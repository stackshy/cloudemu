package asl

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
)

// evalIntrinsic evaluates a first-wave intrinsic invocation such as
// States.Format('{}', $.x). Supported: Format, Array, ArrayGetItem, StringToJson,
// JsonToString, UUID. Unsupported intrinsics fail loudly.
func (it *interp) evalIntrinsic(expr string, input any) (any, error) {
	name, argsStr, err := splitIntrinsic(expr)
	if err != nil {
		return nil, err
	}

	args, err := it.evalArgs(argsStr, input)
	if err != nil {
		return nil, err
	}

	switch name {
	case "States.Format":
		return intrinsicFormat(args)
	case "States.Array":
		return args, nil
	case "States.ArrayGetItem":
		return intrinsicArrayGetItem(args)
	case "States.StringToJson":
		return intrinsicStringToJSON(args)
	case "States.JsonToString":
		return intrinsicJSONToString(args)
	case "States.UUID":
		return newUUID(), nil
	default:
		return nil, aslErrf("unsupported intrinsic function %q", name)
	}
}

// splitIntrinsic separates "States.Func(args...)" into its name and argument
// string.
func splitIntrinsic(expr string) (name, args string, err error) {
	open := strings.IndexByte(expr, '(')
	if open < 0 || !strings.HasSuffix(expr, ")") {
		return "", "", aslErrf("malformed intrinsic %q", expr)
	}

	return expr[:open], expr[open+1 : len(expr)-1], nil
}

// evalArgs splits the top-level, comma-separated arguments and evaluates each.
func (it *interp) evalArgs(argsStr string, input any) ([]any, error) {
	toks := splitArgs(argsStr)

	args := make([]any, 0, len(toks))

	for _, tok := range toks {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}

		v, err := it.evalArg(tok, input)
		if err != nil {
			return nil, err
		}

		args = append(args, v)
	}

	return args, nil
}

func (it *interp) evalArg(tok string, input any) (any, error) {
	switch {
	case strings.HasPrefix(tok, "States."):
		return it.evalIntrinsic(tok, input)
	case strings.HasPrefix(tok, "$"):
		v, present, err := it.resolvePath(tok, input)
		if err != nil {
			return nil, err
		}

		if !present {
			return nil, aslErrf("intrinsic argument path %q not found", tok)
		}

		return v, nil
	case strings.HasPrefix(tok, "'"):
		return unquoteIntrinsic(tok), nil
	default:
		var v any
		if err := json.Unmarshal([]byte(tok), &v); err != nil {
			return nil, aslErrf("invalid intrinsic argument %q", tok)
		}

		return v, nil
	}
}

// splitArgs splits an argument list on top-level commas, respecting single
// quotes and nested parentheses.
func splitArgs(s string) []string {
	var (
		out   []string
		depth int
		inStr bool
		start int
	)

	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && inStr:
			i++
		case c == '\'':
			inStr = !inStr
		case inStr:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}

	return append(out, s[start:])
}

func unquoteIntrinsic(tok string) string {
	tok = strings.TrimPrefix(tok, "'")
	tok = strings.TrimSuffix(tok, "'")
	tok = strings.ReplaceAll(tok, "\\'", "'")

	return strings.ReplaceAll(tok, "\\\\", "\\")
}

func intrinsicFormat(args []any) (any, error) {
	if len(args) == 0 {
		return nil, aslErrf("States.Format requires a template string")
	}

	tmpl, ok := args[0].(string)
	if !ok {
		return nil, aslErrf("States.Format template must be a string")
	}

	out := tmpl

	for _, a := range args[1:] {
		out = strings.Replace(out, emptyObject, stringify(a), 1)
	}

	return out, nil
}

// arrayGetItemArgc is the argument count States.ArrayGetItem(array, index) takes.
const arrayGetItemArgc = 2

func intrinsicArrayGetItem(args []any) (any, error) {
	if len(args) != arrayGetItemArgc {
		return nil, aslErrf("States.ArrayGetItem requires an array and an index")
	}

	arr, ok := args[0].([]any)
	idx, okIdx := toFloat(args[1])

	if !ok || !okIdx || int(idx) < 0 || int(idx) >= len(arr) {
		return nil, aslErrf("States.ArrayGetItem index out of range")
	}

	return arr[int(idx)], nil
}

func intrinsicStringToJSON(args []any) (any, error) {
	if len(args) != 1 {
		return nil, aslErrf("States.StringToJson requires one string argument")
	}

	s, ok := args[0].(string)
	if !ok {
		return nil, aslErrf("States.StringToJson argument must be a string")
	}

	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("States.StringToJson: %w", err)
	}

	return v, nil
}

func intrinsicJSONToString(args []any) (any, error) {
	if len(args) != 1 {
		return nil, aslErrf("States.JsonToString requires one argument")
	}

	b, err := json.Marshal(args[0])
	if err != nil {
		return nil, fmt.Errorf("States.JsonToString: %w", err)
	}

	return string(b), nil
}

// stringify renders an intrinsic argument for States.Format substitution.
func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return toJSON(v)
	}
}

// newUUID returns a random RFC 4122 version-4 UUID. It is non-deterministic,
// which is why interpreter output/history is persisted rather than recomputed.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
