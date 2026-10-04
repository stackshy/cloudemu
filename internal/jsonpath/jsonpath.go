// Package jsonpath evaluates the reference subset of JSONPath shared by the
// Step Functions ASL interpreter and the API Gateway mapping templates: "$",
// "$.a.b", "$[0]", "$['a']" and "$.a.b[2]". Filters, wildcards and recursive
// descent are rejected with an error, so an unsupported path fails loudly
// instead of returning a wrong result.
//
// Values are plain decoded JSON (map[string]any, []any) or any type that
// implements Object or Array, which lets callers keep an order-preserving
// document model.
package jsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

// Object is a JSON object view a path can step into by field name.
type Object interface {
	Lookup(key string) (any, bool)
}

// Array is a JSON array view a path can step into by index.
type Array interface {
	Index(i int) (any, bool)
}

// Error reports a malformed or unsupported path.
type Error struct {
	Msg string
}

func (e *Error) Error() string { return e.Msg }

func errorf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

// Eval evaluates path against root and returns the selected value and whether
// it was present.
func Eval(path string, root any) (value any, present bool, err error) {
	if path == "" || path[0] != '$' {
		return nil, false, errorf("invalid JSONPath %q: must start with '$'", path)
	}

	if strings.ContainsAny(path, "*?@") || strings.Contains(path, "..") {
		return nil, false, errorf("JSONPath %q uses unsupported syntax (filters/wildcards/recursive descent)", path)
	}

	if path == "$" {
		return root, true, nil
	}

	toks, err := tokenize(path[1:])
	if err != nil {
		return nil, false, err
	}

	cur := root

	for _, t := range toks {
		next, ok := t.apply(cur)
		if !ok {
			return nil, false, nil
		}

		cur = next
	}

	return cur, true, nil
}

// token is one selection step: a field name or an array index.
type token struct {
	field   string
	index   int
	isIndex bool
}

func (t token) apply(cur any) (any, bool) {
	if t.isIndex {
		switch arr := cur.(type) {
		case []any:
			if t.index < 0 || t.index >= len(arr) {
				return nil, false
			}

			return arr[t.index], true
		case Array:
			return arr.Index(t.index)
		default:
			return nil, false
		}
	}

	switch obj := cur.(type) {
	case map[string]any:
		v, ok := obj[t.field]

		return v, ok
	case Object:
		return obj.Lookup(t.field)
	default:
		return nil, false
	}
}

// tokenize splits the part of a path after the leading '$' into tokens.
func tokenize(s string) ([]token, error) {
	var toks []token

	for s != "" {
		switch s[0] {
		case '.':
			field, rest := scanField(s[1:])
			if field == "" {
				return nil, errorf("empty field name in JSONPath")
			}

			toks = append(toks, token{field: field})
			s = rest
		case '[':
			tok, rest, err := scanBracket(s)
			if err != nil {
				return nil, err
			}

			toks = append(toks, tok)
			s = rest
		default:
			return nil, errorf("unexpected character %q in JSONPath", s[0])
		}
	}

	return toks, nil
}

// scanField reads a dotted field name up to the next '.' or '['.
func scanField(s string) (field, rest string) {
	i := strings.IndexAny(s, ".[")
	if i < 0 {
		return s, ""
	}

	return s[:i], s[i:]
}

// scanBracket reads a "[...]" selector: a numeric index or a quoted field name.
func scanBracket(s string) (token, string, error) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return token{}, "", errorf("unterminated '[' in JSONPath")
	}

	inner := s[1:end]
	rest := s[end+1:]

	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') {
		return token{field: inner[1 : len(inner)-1]}, rest, nil
	}

	idx, err := strconv.Atoi(inner)
	if err != nil {
		return token{}, "", errorf("invalid array index %q in JSONPath", inner)
	}

	return token{index: idx, isIndex: true}, rest, nil
}
