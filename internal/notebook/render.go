package notebook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"sort"
	"text/template"
	"text/template/parse"
)

// Render executes a notebook SQL template. Values reach the SQL only through
// {{ param ... }}, which binds them; mugo functions (see funcs.go) can shape
// a value first, as in {{ param (.data.name | trim | upper) }}. Printing any
// other value into the SQL text is refused, so input never becomes SQL.
func Render(sql string, data, cells map[string]any, placeholder func(int) string) (string, []any, error) {
	return RenderContext(context.Background(), sql, data, cells, placeholder)
}

func RenderContext(ctx context.Context, sql string, data, cells map[string]any, placeholder func(int) string) (string, []any, error) {
	var args []any
	var rangeErr error
	iterations := 0
	// bounded walks a range value in a stable order, stopping on
	// cancellation or when the iteration budget of the render is spent.
	bounded := func(v any) (func(yield func(any, any) bool), error) {
		rv := reflect.ValueOf(v)
		if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array && rv.Kind() != reflect.Map) {
			return nil, errors.New("range requires an array or object, not an integer or function")
		}
		if rv.Len() > 10000 {
			return nil, errors.New("template range exceeds 10000 values")
		}
		return func(yield func(any, any) bool) {
			step := func(k, val any) bool {
				iterations++
				if err := ctx.Err(); err != nil {
					rangeErr = err
					return false
				}
				if iterations > 10000 {
					rangeErr = errors.New("template iteration budget exceeded")
					return false
				}
				return yield(k, val)
			}
			if rv.Kind() == reflect.Map {
				keys := rv.MapKeys()
				sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
				for _, k := range keys {
					if !step(k.Interface(), rv.MapIndex(k).Interface()) {
						return
					}
				}
			} else {
				for i := 0; i < rv.Len(); i++ {
					if !step(i, rv.Index(i).Interface()) {
						return
					}
				}
			}
		}, nil
	}
	funcs := TemplateFuncs()
	for name, fn := range map[string]any{
		"param": func(v any) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			bound, err := bindValue(v)
			if err != nil {
				return "", err
			}
			args = append(args, bound)
			return placeholder(len(args)), nil
		},
		// text/template binds the key of an iter.Seq2 when a range declares
		// fewer than two variables, so those ranges iterate values only.
		"boundedValues": func(v any) (iter.Seq[any], error) {
			seq, err := bounded(v)
			if err != nil {
				return nil, err
			}
			return func(yield func(any) bool) {
				seq(func(_, val any) bool { return yield(val) })
			}, nil
		},
		"boundedRange": func(v any) (iter.Seq2[any, any], error) {
			seq, err := bounded(v)
			if err != nil {
				return nil, err
			}
			return iter.Seq2[any, any](seq), nil
		},
	} {
		funcs[name] = fn
	}

	t, err := template.New("sql").Option("missingkey=error").Funcs(funcs).Parse(sql)
	if err != nil {
		return "", nil, err
	}
	if len(t.Templates()) != 1 {
		return "", nil, errors.New("template definitions are not supported")
	}
	if err := safeNodes(t.Tree.Root); err != nil { //nolint:staticcheck
		return "", nil, err
	}
	b := &boundedBuffer{limit: 1 << 20, ctx: ctx}
	if err := t.Execute(b, map[string]any{"data": data, "cells": cells}); err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if rangeErr != nil {
		return "", nil, rangeErr
	}
	return b.String(), args, nil
}

func safeNodes(n parse.Node) error { return safeNodesDepth(n, 0) }
func safeNodesDepth(n parse.Node, ranges int) error {
	if n == nil {
		return nil
	}
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, child := range n.Nodes {
			if err := safeNodesDepth(child, ranges); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		if err := checkPipe(n.Pipe); err != nil {
			return err
		}
		if len(n.Pipe.Decl) > 0 {
			return paramFree(n.Pipe)
		}
		// The last command of a printed pipeline must be param, so
		// {{ param .x }} and {{ .x | upper | param }} both bind the value.
		cmds := n.Pipe.Cmds
		last := cmds[len(cmds)-1]
		id, ok := last.Args[0].(*parse.IdentifierNode)
		if !ok || id.Ident != "param" {
			return errors.New("SQL template values must use {{ param ... }}; do not quote parameters; identifiers must be static SQL")
		}
		for _, cmd := range cmds[:len(cmds)-1] {
			if err := paramFreeArgs(cmd.Args); err != nil {
				return err
			}
		}
		return paramFreeArgs(last.Args[1:])
	case *parse.IfNode:
		if err := checkPipe(n.Pipe); err != nil {
			return err
		}
		if err := paramFree(n.Pipe); err != nil {
			return err
		}
		if err := safeNodesDepth(n.List, ranges); err != nil {
			return err
		}
		return safeNodesDepth(n.ElseList, ranges)
	case *parse.RangeNode:
		if err := checkPipe(n.Pipe); err != nil {
			return err
		}
		if err := paramFree(n.Pipe); err != nil {
			return err
		}
		iterate := "boundedValues"
		if len(n.Pipe.Decl) > 1 {
			iterate = "boundedRange"
		}
		n.Pipe.Cmds = append(n.Pipe.Cmds, &parse.CommandNode{NodeType: parse.NodeCommand, Args: []parse.Node{parse.NewIdentifier(iterate)}})
		if ranges > 0 {
			return errors.New("nested template ranges are not supported")
		}
		if err := safeNodesDepth(n.List, ranges+1); err != nil {
			return err
		}
		return safeNodesDepth(n.ElseList, ranges+1)
	case *parse.WithNode:
		if err := checkPipe(n.Pipe); err != nil {
			return err
		}
		if err := paramFree(n.Pipe); err != nil {
			return err
		}
		if err := safeNodesDepth(n.List, ranges); err != nil {
			return err
		}
		return safeNodesDepth(n.ElseList, ranges)
	case *parse.TemplateNode:
		return errors.New("recursive templates are not supported")
	}
	return nil
}

// paramFree refuses param where its placeholder would not be printed into
// the SQL, which would bind a value without a place for it.
func paramFree(p *parse.PipeNode) error {
	if p == nil {
		return nil
	}
	for _, cmd := range p.Cmds {
		if err := paramFreeArgs(cmd.Args); err != nil {
			return err
		}
	}
	return nil
}

func paramFreeArgs(args []parse.Node) error {
	for _, arg := range args {
		switch arg := arg.(type) {
		case *parse.IdentifierNode:
			if arg.Ident == "param" {
				return errors.New("param must end a printed {{ ... }} action, not sit inside a condition, variable or argument")
			}
		case *parse.PipeNode:
			if err := paramFree(arg); err != nil {
				return err
			}
		case *parse.ChainNode:
			if pipe, ok := arg.Node.(*parse.PipeNode); ok {
				if err := paramFree(pipe); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
	ctx   context.Context
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.ctx != nil {
		if err := b.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("rendered SQL exceeds %d bytes: %w", b.limit, io.ErrShortBuffer)
	}
	return b.Buffer.Write(p)
}
