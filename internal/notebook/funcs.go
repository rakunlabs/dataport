package notebook

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"text/template/parse"
	"time"

	"github.com/rytsh/mugo/fstore"
	"github.com/spf13/cast"

	// Only side-effect-free function packages are registered. exec, file,
	// os, log and template execution are never imported.
	_ "github.com/rytsh/mugo/fstore/registry/cast"
	_ "github.com/rytsh/mugo/fstore/registry/codec"
	_ "github.com/rytsh/mugo/fstore/registry/crypto"
	_ "github.com/rytsh/mugo/fstore/registry/sprig"
	_ "github.com/rytsh/mugo/fstore/registry/time"
)

// Template functions come from mugo, restricted to functions that cannot
// reach the environment, files, network or processes, and whose output grows
// at most linearly with their input. Functions that take a repeat count, an
// iteration range or a key size (repeat, until, seq, indent, wrap, printf,
// randBytes, genPrivateKey, bcrypt…) are excluded, because template
// execution cannot be interrupted inside a function call. mugo's math
// namespace is excluded too: its arbitrary-precision decimals turn an input
// such as "1e999999999" into a billion-digit string; sprig's add, mulf,
// round and friends cover arithmetic with bounded int64 and float64 values.
var mugoFunctions = []string{
	// strings
	"trim", "trimAll", "trimPrefix", "trimSuffix", "upper", "lower", "title", "untitle",
	"substr", "trunc", "abbrev", "abbrevboth", "nospace", "initials", "swapcase",
	"snakecase", "camelcase", "kebabcase", "contains", "hasPrefix", "hasSuffix",
	"quote", "squote", "cat", "plural", "toString", "splitList", "join", "sortAlpha",
	"regexMatch", "regexFind", "regexFindAll", "regexSplit",
	// numbers
	"atoi", "int", "int64", "float64", "toDecimal", "add", "add1", "sub", "mul", "div", "mod",
	"max", "min", "addf", "add1f", "subf", "mulf", "divf", "maxf", "minf", "round", "floor", "ceil",
	// defaults and logic
	"default", "empty", "coalesce", "ternary", "all", "any", "compact",
	// data structures
	"list", "dict", "get", "hasKey", "keys", "values", "pick", "omit", "dig",
	"first", "last", "rest", "initial", "reverse", "has",
	// json and encoding
	"toJson", "toRawJson", "fromJson", "b64enc", "b64dec", "b32enc", "b32dec",
	"sha1sum", "sha256sum", "sha512sum", "adler32sum",
	// dates
	"now", "date", "dateInZone", "toDate", "mustToDate", "unixEpoch", "duration", "durationRound", "dateModify", "ago",
	"uuidv4",
	// mugo namespaces, their methods are checked below
	"cast", "codec", "crypto", "time",
}

// mugoMethods lists the methods of the mugo namespaces a template may call,
// as in {{ cast.ToInt .data.id }}.
var mugoMethods = map[string]map[string]bool{
	"cast": set("ToBool", "ToFloat64", "ToInt", "ToInt64", "ToString", "ToSlice", "ToStringSlice",
		"ToStringMap", "ToIntSlice", "ToDuration", "StringToDate"),
	"codec":  set("JsonDecode", "JsonEncode", "ByteToString", "StringToByte"),
	"crypto": set("Base64", "Base64Decode", "MD5", "SHA1", "SHA256", "FNV32a", "HMAC", "JwtParseUnverified"),
	"time":   set("Now", "RFC3339", "Format", "UTC", "AddDuration", "Duration", "AddDate"),
}

// Built-in template functions allowed besides mugo's.
var builtinFunctions = set("index", "len", "slice", "eq", "ne", "lt", "le", "gt", "ge", "and", "or", "not")

var templateFuncs = func() map[string]any {
	funcs := fstore.FuncMap(fstore.WithTrust(false), fstore.WithSpecificFuncs(mugoFunctions...))
	// sprig's replace grows its result by the number of matches; this one
	// refuses results above the limit before building them. join is wrapped
	// for the same reason: a long separator between many items multiplies.
	funcs["replace"] = replace
	funcs["join"] = join

	return funcs
}()

func join(sep string, v any) (string, error) {
	items := cast.ToStringSlice(v)
	if len(items) > 1 && len(sep) > maxValueBytes/(len(items)-1) {
		return "", errors.New("join: result exceeds 1 MiB")
	}

	total := len(sep) * max(0, len(items)-1)
	for _, s := range items {
		if total += len(s); total > maxValueBytes {
			return "", errors.New("join: result exceeds 1 MiB")
		}
	}

	return strings.Join(items, sep), nil
}

var allowedFunctions = func() map[string]bool {
	m := map[string]bool{"replace": true, "join": true}
	for name := range templateFuncs {
		if _, namespace := mugoMethods[name]; !namespace {
			m[name] = true
		}
	}

	for name := range builtinFunctions {
		m[name] = true
	}

	return m
}()

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}

	return m
}

const maxValueBytes = 1 << 20

func replace(old, replacement, src string) (string, error) {
	if len(src) > maxValueBytes || len(replacement) > maxValueBytes {
		return "", errors.New("replace: value exceeds 1 MiB")
	}

	if growth := len(replacement) - len(old); growth > 0 && old != "" {
		if strings.Count(src, old) > (maxValueBytes-len(src))/growth {
			return "", errors.New("replace: result exceeds 1 MiB")
		}
	} else if old == "" && growth > 0 && (len(src)+1)*growth > maxValueBytes {
		return "", errors.New("replace: result exceeds 1 MiB")
	}

	return strings.ReplaceAll(src, old, replacement), nil
}

// TemplateFuncs returns the functions SQL and mapping templates may use.
func TemplateFuncs() map[string]any {
	out := make(map[string]any, len(templateFuncs))
	for k, v := range templateFuncs {
		out[k] = v
	}

	return out
}

// checkPipe refuses any function, namespace method or call the allowlist
// does not name. Namespaces are only usable as namespace.Method, never as
// values, so a template cannot reach methods through a variable.
func checkPipe(p *parse.PipeNode) error { return checkPipeWith(p, nil) }

// checkPipeWith also accepts the functions named in extra, which the
// caller adds to the template.
func checkPipeWith(p *parse.PipeNode, extra map[string]bool) error {
	if p == nil {
		return nil
	}

	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			if err := checkArg(arg, extra); err != nil {
				return err
			}
		}
	}

	return nil
}

func checkArg(arg parse.Node, extra map[string]bool) error {
	switch arg := arg.(type) {
	case *parse.IdentifierNode:
		if arg.Ident == "param" || extra[arg.Ident] {
			return nil
		}

		if _, namespace := mugoMethods[arg.Ident]; namespace {
			return errors.New("use " + arg.Ident + ".Method, for example cast.ToInt")
		}

		if !allowedFunctions[arg.Ident] {
			return errors.New("unsupported template function: " + arg.Ident)
		}
	case *parse.ChainNode:
		id, ok := arg.Node.(*parse.IdentifierNode)
		if !ok {
			if pipe, ok := arg.Node.(*parse.PipeNode); ok {
				if err := checkPipeWith(pipe, extra); err != nil {
					return err
				}

				return nil
			}

			return nil
		}

		methods, namespace := mugoMethods[id.Ident]
		if !namespace || len(arg.Field) != 1 || !methods[arg.Field[0]] {
			return errors.New("unsupported template function: " + arg.String())
		}
	case *parse.PipeNode:
		return checkPipeWith(arg, extra)
	}

	return nil
}

// ValidateMappingTemplate checks a transfer value template: the same
// functions as SQL templates, without ranges or template calls. Its output
// is a value, not SQL, so it may print freely.
func ValidateMappingTemplate(n parse.Node) error {
	return ValidateValueTemplate(n)
}

// ValidateValueTemplate checks a template whose output is a value, as
// ValidateMappingTemplate does, also accepting the extra functions the
// caller adds to it.
func ValidateValueTemplate(n parse.Node, extra ...string) error {
	return validateValue(n, set(extra...))
}

func validateValue(n parse.Node, extra map[string]bool) error {
	switch n := n.(type) {
	case nil:
		return nil
	case *parse.ListNode:
		if n == nil {
			return nil
		}

		for _, child := range n.Nodes {
			if err := validateValue(child, extra); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return checkPipeWith(n.Pipe, extra)
	case *parse.IfNode:
		if err := checkPipeWith(n.Pipe, extra); err != nil {
			return err
		}

		if err := validateValue(n.List, extra); err != nil {
			return err
		}

		return validateValue(n.ElseList, extra)
	case *parse.WithNode:
		if err := checkPipeWith(n.Pipe, extra); err != nil {
			return err
		}

		if err := validateValue(n.List, extra); err != nil {
			return err
		}

		return validateValue(n.ElseList, extra)
	case *parse.TemplateNode, *parse.RangeNode:
		return errors.New("value templates cannot call or loop templates")
	}

	return nil
}

// bindValue turns what a template function returns into a value the SQL
// drivers bind.
func bindValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, string, bool, int64, float64, time.Time, []byte:
		return x, nil
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case float32:
		return float64(x), nil
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return i, nil
		}

		// Decimals keep every digit as text; the database converts them.
		return string(x), nil
	case time.Duration:
		return x.String(), nil
	}

	return nil, errors.New("param expects a scalar value; use index or a function to select one")
}
