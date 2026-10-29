package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

type QueryTransferRequest struct {
	SQL    string
	Args   []any
	Target notebook.Transfer
	// SourceTimeout limits the source query, Timeout the target session.
	SourceTimeout time.Duration
	Timeout       time.Duration
}
type QueryTransferResult struct {
	Inserted   int64 `json:"inserted"`
	Skipped    int64 `json:"skipped"`
	DurationMS int64 `json:"duration_ms"`
}

// QueryRows runs the source query of a query transfer and calls fn with
// the target columns; rows hands out the mapped rows one by one. It runs on
// this instance or, in a cluster, on the replica holding the source pool.
type QueryRows func(ctx context.Context, fn func(ctx context.Context, targets []string, rows func(RowFunc) error) error) error

// QueryTransfer streams a read-only SELECT to an existing target table using
// bound inserts. The destination is one transaction. No distributed atomicity
// is claimed. Separate pools/connections are needed even on the same DB.
func (m *Manager) QueryTransfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string, req QueryTransferRequest) (*QueryTransferResult, error) {
	return m.QueryTransferFrom(ctx, src, m.QuerySource(src, srcPassword, req), dst, dstPassword, req)
}

// QuerySource reads the source query of req from c on this instance.
func (m *Manager) QuerySource(src *store.Connection, srcPassword string, req QueryTransferRequest) QueryRows {
	return func(ctx context.Context, fn func(ctx context.Context, targets []string, rows func(RowFunc) error) error) error {
		sd, err := DriverFor(src)
		if err != nil {
			return err
		}
		stmts, err := Validate(sd, req.SQL, true)
		if err != nil {
			return err
		}
		if len(stmts) != 1 || !returnsRows(stmts[0], FirstKeyword(stmts[0], sd.Dialect())) {
			return errors.New("transfer source must be one read-only query")
		}
		return m.Session(ctx, src, srcPassword, req.SourceTimeout, func(ctx context.Context, sd Driver, source *sql.Conn) error {
			if cp, ok := sd.(ContextPreparer); ok {
				ctx = cp.PrepareContext(ctx, req.SourceTimeout, true)
			}
			var reader Runner = source
			if useTransaction(sd, true) {
				tx, err := beginTx(ctx, sd, source, true)
				if err != nil {
					return err
				}
				defer tx.Rollback() //nolint:errcheck
				reader = tx
			}
			if err := sd.Prepare(ctx, reader, req.SourceTimeout); err != nil {
				return err
			}
			rows, err := reader.QueryContext(ctx, stmts[0], req.Args...)
			if err != nil {
				return err
			}
			defer rows.Close() //nolint:errcheck
			columns, err := rows.Columns()
			if err != nil {
				return err
			}
			mappings, err := transferMappings(req.Target, columns)
			if err != nil {
				return err
			}
			indexes, transforms, targets := make([]int, len(mappings)), make([]*template.Template, len(mappings)), make([]string, len(mappings))
			for i, mapping := range mappings {
				indexes[i] = -1
				for j, col := range columns {
					if col == mapping.Source {
						if indexes[i] != -1 {
							return errors.New("ambiguous source column " + col)
						}
						indexes[i] = j
					}
				}
				if indexes[i] < 0 {
					return errors.New("source column not found: " + mapping.Source)
				}
				targets[i] = mapping.Target
				if mapping.Template != "" {
					t, err := template.New("value").Funcs(notebook.TemplateFuncs()).Parse(mapping.Template)
					if err != nil {
						return err
					}
					if len(t.Templates()) != 1 {
						return errors.New("mapping templates cannot define templates")
					}
					if err := notebook.ValidateMappingTemplate(t.Tree.Root); err != nil { //nolint:staticcheck
						return err
					}
					transforms[i] = t
				}
			}
			return fn(ctx, targets, func(f RowFunc) error {
				values, pointers := make([]any, len(columns)), make([]any, len(columns))
				for i := range values {
					pointers[i] = &values[i]
				}
				for rows.Next() {
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := rows.Scan(pointers...); err != nil {
						return err
					}
					row := make([]any, len(mappings))
					for i, mapping := range mappings {
						v, err := mapTransferValue(values[indexes[i]], mapping, transforms[i])
						if err != nil {
							return fmt.Errorf("column %s: %w", mapping.Source, err)
						}
						row[i] = v
					}
					if err := f(row); err != nil {
						return err
					}
				}
				return rows.Err()
			})
		})
	}
}

// QueryTransferFrom is QueryTransfer reading the rows from source.
func (m *Manager) QueryTransferFrom(ctx context.Context, src *store.Connection, source QueryRows, dst *store.Connection, dstPassword string, req QueryTransferRequest) (*QueryTransferResult, error) {
	if dst.ReadOnly {
		return nil, ErrReadOnly
	}
	if src.ID == dst.ID {
		return nil, errors.New("query transfer needs distinct source and target connections")
	}
	start := time.Now()
	out := &QueryTransferResult{}
	err := source(ctx, func(ctx context.Context, targets []string, rows func(RowFunc) error) error {
		return m.Session(ctx, dst, dstPassword, req.Timeout, func(ctx context.Context, td Driver, target *sql.Conn) error {
			if !transactional(td) || !useTransaction(td, false) {
				return errors.New("query transfer requires a transactional target")
			}
			// Savepoint syntax is deliberately restricted to tested dialects.
			if req.Target.SkipError != "" && td.Type() != "postgres" && td.Type() != "mysql" && td.Type() != "sqlite" {
				return errors.New("skip-error is supported on PostgreSQL, MySQL and SQLite targets")
			}
			return inTransaction(ctx, td, target, req.Timeout, func(run Runner) error {
				if req.Target.Wipe {
					if _, err := run.ExecContext(ctx, "DELETE FROM "+qualify(td, req.Target.Schema, req.Target.Table)); err != nil {
						return err
					}
				}
				ins := NewInserter(ctx, td, run, req.Target.Schema, req.Target.Table, targets)
				ins.perStmt = min(ins.perStmt, max(1, req.Target.Batch))
				batch := make([][]any, 0, ins.perStmt)
				insert := func(values [][]any) error { ins.batch = values; return ins.Flush() }
				attempt := func(values [][]any) (error, error) {
					if req.Target.SkipError == "" {
						return insert(values), nil
					}
					if _, err := run.ExecContext(ctx, "SAVEPOINT dataport_transfer"); err != nil {
						return nil, err
					}
					err := insert(values)
					if err != nil {
						if _, control := run.ExecContext(ctx, "ROLLBACK TO SAVEPOINT dataport_transfer"); control != nil {
							return err, control
						}
					}
					_, control := run.ExecContext(ctx, "RELEASE SAVEPOINT dataport_transfer")
					return err, control
				}
				flush := func() error {
					if len(batch) == 0 {
						return nil
					}
					err, control := attempt(batch)
					if control != nil {
						return control
					}
					if err == nil {
						out.Inserted += int64(len(batch))
						batch = batch[:0]
						return nil
					}
					if req.Target.SkipError == "" || !strings.Contains(err.Error(), req.Target.SkipError) {
						return err
					}
					for _, row := range batch {
						err, control := attempt([][]any{row})
						if control != nil {
							return control
						}
						if err != nil {
							if !strings.Contains(err.Error(), req.Target.SkipError) {
								return err
							}
							out.Skipped++
						} else {
							out.Inserted++
						}
					}
					batch = batch[:0]
					return nil
				}
				if err := rows(func(row []any) error {
					batch = append(batch, row)
					if len(batch) >= ins.perStmt {
						return flush()
					}
					return nil
				}); err != nil {
					return err
				}
				return flush()
			})
		})
	})
	out.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		out.Inserted = 0
	}
	return out, err
}

// transferMappings resolves the mappings of a transfer against the source
// columns: without mappings every column copies under its own name, and
// with KeepUnmapped the unmapped columns are added in source order. A
// source column may feed several targets; a target is written only once.
func transferMappings(t notebook.Transfer, columns []string) ([]notebook.Mapping, error) {
	mappings := t.Mappings
	if t.KeepUnmapped && len(mappings) > 0 {
		bySource := map[string][]notebook.Mapping{}
		for _, mapping := range mappings {
			bySource[mapping.Source] = append(bySource[mapping.Source], mapping)
		}
		mappings = nil
		for _, col := range columns {
			mapped, ok := bySource[col]
			if !ok {
				mapped = []notebook.Mapping{{Source: col, Target: col, Nullable: true}}
			}
			mappings = append(mappings, mapped...)
			delete(bySource, col)
		}
		for source := range bySource {
			return nil, errors.New("source column not found: " + source)
		}
	}
	if len(mappings) == 0 {
		for _, name := range columns {
			mappings = append(mappings, notebook.Mapping{Source: name, Target: name, Nullable: true})
		}
	}
	targets := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		if targets[mapping.Target] {
			return nil, errors.New("target column " + mapping.Target + " is written twice; map or rename the source column")
		}
		targets[mapping.Target] = true
	}
	return mappings, nil
}

func mapTransferValue(v any, mapping notebook.Mapping, transform *template.Template) (any, error) {
	if v == nil {
		if mapping.Nullable {
			return nil, nil
		}
		return nil, errors.New("NULL is not allowed by this mapping")
	}
	if b, ok := v.([]byte); ok {
		if mapping.Encoding == "ISO-8859-1" {
			r := make([]rune, len(b))
			for i, c := range b {
				r[i] = rune(c)
			}
			v = string(r)
		} else if mapping.Type != "" || transform != nil {
			v = string(b)
		} else {
			return bytes.Clone(b), nil
		}
	}
	if transform != nil {
		if len(fmt.Sprint(v)) > 1<<20 {
			return nil, errors.New("mapping input exceeds 1 MiB")
		}
		var b mappingBuffer
		if err := transform.Execute(&b, v); err != nil {
			return nil, err
		}
		v = b.String()
	}
	switch mapping.Type {
	case "string":
		return fmt.Sprint(v), nil
	case "number":
		s := fmt.Sprint(v)
		if len(s) > 256 {
			return nil, errors.New("number is too long")
		}
		if !transferDecimal.MatchString(s) {
			return nil, errors.New("invalid decimal number")
		}
		return s, nil
	case "date":
		if _, ok := v.(time.Time); ok {
			return v, nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, fmt.Sprint(v)); err == nil {
				return t, nil
			}
		}
		return nil, errors.New("invalid date")
	}
	return v, nil
}

var transferDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,4})?$`)

type mappingBuffer struct{ bytes.Buffer }

func (b *mappingBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("mapped value exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}
