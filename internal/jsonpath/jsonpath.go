// Package jsonpath evaluates JSONPath expressions over decoded JSON.
//
// Eval handles the definite subset the Step Functions ASL interpreter uses:
// "$", "$.a.b", "$[0]", "$['a']" and "$.a.b[2]". Filters, wildcards and
// recursive descent are rejected with an error, so an unsupported path fails
// loudly instead of returning a wrong result.
//
// EvalAll also accepts the wildcard ("[*]", ".*") and recursive descent
// ("..name", "..*") forms the API Gateway mapping templates allow, and returns
// every match. Filters stay unsupported.
//
// Values are plain decoded JSON (map[string]any, []any) or any type that
// implements Object or Array, which lets callers keep an order-preserving
// document model.
package jsonpath

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Object is a JSON object view a path can step into by field name.
type Object interface {
	Lookup(key string) (any, bool)
	Keys() []string
}

// Array is a JSON array view a path can step into by index.
type Array interface {
	Index(i int) (any, bool)
	Len() int
}

// maxDepth bounds how deep recursive descent walks, so a pathological document
// cannot exhaust the stack.
const maxDepth = 1000

// maxMatches caps the values one EvalAll step may collect, so chained
// wildcards and descents cannot multiply a document into a huge result.
const maxMatches = 1 << 20

// Error reports a malformed or unsupported path.
type Error struct {
	Msg string
}

func (e *Error) Error() string { return e.Msg }

func errorf(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

// Eval evaluates a definite path against root and returns the selected value
// and whether it was present.
func Eval(path string, root any) (value any, present bool, err error) {
	if rootErr := checkRoot(path); rootErr != nil {
		return nil, false, rootErr
	}

	if strings.ContainsAny(path, "*?@") || strings.Contains(path, "..") {
		return nil, false, errorf("JSONPath %q uses unsupported syntax (filters/wildcards/recursive descent)", path)
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

// EvalAll evaluates a path that may use wildcards or recursive descent.
// indefinite reports whether the path can match more than one value (it uses
// a wildcard or descent), in which case callers present the matches as a list.
// For a definite path values holds at most one element.
func EvalAll(path string, root any) (values []any, indefinite bool, err error) {
	if rootErr := checkRoot(path); rootErr != nil {
		return nil, false, rootErr
	}

	if strings.ContainsAny(path, "?@") {
		return nil, false, errorf("JSONPath %q uses unsupported syntax (filters)", path)
	}

	toks, err := tokenize(path[1:])
	if err != nil {
		return nil, false, err
	}

	cur := []any{root}

	for _, t := range toks {
		indefinite = indefinite || t.wildcard || t.descent

		var next []any

		for _, v := range cur {
			next = t.collect(v, next)

			if len(next) > maxMatches {
				return nil, true, errorf("JSONPath %q matches more than %d values", path, maxMatches)
			}
		}

		cur = next
	}

	return cur, indefinite, nil
}

func checkRoot(path string) error {
	if path == "" || path[0] != '$' {
		return errorf("invalid JSONPath %q: must start with '$'", path)
	}

	return nil
}

// token is one selection step: a field name, an array index or a wildcard,
// optionally applied at every depth (descent).
type token struct {
	field    string
	index    int
	isIndex  bool
	wildcard bool
	descent  bool
}

// apply selects one child of cur (definite tokens only).
func (t token) apply(cur any) (any, bool) {
	if t.isIndex {
		return elem(cur, t.index)
	}

	return field(cur, t.field)
}

// collect appends every match of t under v to out.
func (t token) collect(v any, out []any) []any {
	if !t.descent {
		return t.collectHere(v, out)
	}

	// Walk v and all of its descendants in document order.
	type frame struct {
		v     any
		depth int
	}

	stack := []frame{{v, 0}}

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		out = t.collectHere(f.v, out)

		if f.depth >= maxDepth {
			continue
		}

		kids := children(f.v)
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, frame{kids[i], f.depth + 1})
		}
	}

	return out
}

// collectHere appends the matches of t directly under v.
func (t token) collectHere(v any, out []any) []any {
	switch {
	case t.wildcard:
		return append(out, children(v)...)
	case t.isIndex:
		if got, ok := elem(v, t.index); ok {
			return append(out, got)
		}
	default:
		if got, ok := field(v, t.field); ok {
			return append(out, got)
		}
	}

	return out
}

func elem(cur any, i int) (any, bool) {
	switch arr := cur.(type) {
	case []any:
		if i < 0 || i >= len(arr) {
			return nil, false
		}

		return arr[i], true
	case Array:
		return arr.Index(i)
	default:
		return nil, false
	}
}

func field(cur any, name string) (any, bool) {
	switch obj := cur.(type) {
	case map[string]any:
		v, ok := obj[name]

		return v, ok
	case Object:
		return obj.Lookup(name)
	default:
		return nil, false
	}
}

// children returns an object's values (in key order) or an array's elements.
func children(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case Array:
		out := make([]any, 0, t.Len())

		for i := range t.Len() {
			e, _ := t.Index(i)
			out = append(out, e)
		}

		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, t[k])
		}

		return out
	case Object:
		keys := t.Keys()
		out := make([]any, 0, len(keys))

		for _, k := range keys {
			e, _ := t.Lookup(k)
			out = append(out, e)
		}

		return out
	default:
		return nil
	}
}

// tokenize splits the part of a path after the leading '$' into tokens.
func tokenize(s string) ([]token, error) {
	var toks []token

	for s != "" {
		var (
			tok token
			err error
		)

		switch {
		case strings.HasPrefix(s, ".."):
			tok, s, err = scanStep(s[2:])
			tok.descent = true
		case s[0] == '.':
			tok, s, err = scanStep(s[1:])
		case s[0] == '[':
			tok, s, err = scanBracket(s)
		default:
			return nil, errorf("unexpected character %q in JSONPath", s[0])
		}

		if err != nil {
			return nil, err
		}

		toks = append(toks, tok)
	}

	return toks, nil
}

// scanStep reads the selector after a '.' or '..': a field name, '*' or a
// bracket.
func scanStep(s string) (token, string, error) {
	if strings.HasPrefix(s, "[") {
		return scanBracket(s)
	}

	i := strings.IndexAny(s, ".[")
	if i < 0 {
		i = len(s)
	}

	name := s[:i]

	switch name {
	case "":
		return token{}, "", errorf("empty field name in JSONPath")
	case "*":
		return token{wildcard: true}, s[i:], nil
	default:
		return token{field: name}, s[i:], nil
	}
}

// scanBracket reads a "[...]" selector: a numeric index, '*' or a quoted
// field name.
func scanBracket(s string) (token, string, error) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return token{}, "", errorf("unterminated '[' in JSONPath")
	}

	inner := s[1:end]
	rest := s[end+1:]

	if inner == "*" {
		return token{wildcard: true}, rest, nil
	}

	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') {
		return token{field: inner[1 : len(inner)-1]}, rest, nil
	}

	idx, err := strconv.Atoi(inner)
	if err != nil {
		return token{}, "", errorf("invalid array index %q in JSONPath", inner)
	}

	return token{index: idx, isIndex: true}, rest, nil
}
