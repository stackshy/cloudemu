package filterpattern

import (
	"encoding/json"
	"regexp"
	"strconv"
)

// valueSpec is the right-hand side of a comparison.
type valueSpec struct {
	text  string
	re    *regexp.Regexp // %regex% value
	glob  *regexp.Regexp // literal or * wildcard value
	isNum bool
	num   float64
}

type cmpNode struct {
	sel token
	op  string
	val *valueSpec
}

func (n cmpNode) selectors() []token { return []token{n.sel} }

// eval is true when any value the selector reaches satisfies the comparison.
// A selector that reaches nothing, or only objects and arrays, never matches.
func (n cmpNode) eval(doc any) bool {
	for _, v := range resolve(doc, n.sel.steps) {
		if isScalar(v) && n.holds(v) {
			return true
		}
	}

	return false
}

func (n cmpNode) holds(v any) bool {
	switch n.op {
	case opEq:
		return n.equals(v)
	case opNe:
		return !n.equals(v)
	}

	f, ok := v.(float64)
	if !ok {
		return false
	}

	switch n.op {
	case opLt:
		return f < n.val.num
	case opLe:
		return f <= n.val.num
	case opGt:
		return f > n.val.num
	default:
		return f >= n.val.num
	}
}

func (n cmpNode) equals(v any) bool {
	if n.val.re != nil {
		return n.val.re.MatchString(scalarString(v))
	}

	if f, ok := v.(float64); ok && n.val.isNum {
		return f == n.val.num
	}

	return n.val.glob.MatchString(scalarString(v))
}

type isNode struct {
	sel  token
	kind string
}

func (n isNode) selectors() []token { return []token{n.sel} }

func (n isNode) eval(doc any) bool {
	for _, v := range resolve(doc, n.sel.steps) {
		switch n.kind {
		case kwNull:
			if v == nil {
				return true
			}
		default:
			if b, ok := v.(bool); ok && b == (n.kind == kwTrue) {
				return true
			}
		}
	}

	return false
}

type notExistsNode struct{ sel token }

func (n notExistsNode) selectors() []token { return []token{n.sel} }

func (n notExistsNode) eval(doc any) bool { return len(resolve(doc, n.sel.steps)) == 0 }

// match parses the message as JSON; a message that is not a JSON document
// never matches a JSON pattern.
func (p *jsonPattern) match(message string) bool {
	doc, ok := decodeJSON(message)

	return ok && p.root.eval(doc)
}

// extract returns the scalar each non-wildcard selector reaches in message.
func (p *jsonPattern) extract(message string) map[string]string {
	out := map[string]string{}

	doc, ok := decodeJSON(message)
	if !ok {
		return out
	}

	for _, sel := range p.root.selectors() {
		vals := resolve(doc, sel.steps)
		if len(vals) == 1 && isScalar(vals[0]) && !hasWildcard(sel.steps) {
			out[sel.text] = scalarString(vals[0])
		}
	}

	return out
}

func decodeJSON(message string) (any, bool) {
	var doc any
	if err := json.Unmarshal([]byte(message), &doc); err != nil {
		return nil, false
	}

	return doc, true
}

// resolve walks the selector steps from doc and returns every value reached.
// A JSON null that is present is returned as nil, so it differs from a
// missing property, which contributes nothing.
func resolve(doc any, steps []step) []any {
	cur := []any{doc}

	for _, st := range steps {
		next := make([]any, 0, len(cur))

		for _, v := range cur {
			next = append(next, applyStep(v, st)...)
		}

		cur = next
	}

	return cur
}

func applyStep(v any, st step) []any {
	switch st.kind {
	case stepField:
		if m, ok := v.(map[string]any); ok {
			if child, found := m[st.field]; found {
				return []any{child}
			}
		}
	case stepIndex:
		if a, ok := v.([]any); ok && st.index < len(a) {
			return []any{a[st.index]}
		}
	case stepWildcard:
		return children(v)
	}

	return nil
}

func children(v any) []any {
	switch c := v.(type) {
	case map[string]any:
		out := make([]any, 0, len(c))
		for _, x := range c {
			out = append(out, x)
		}

		return out
	case []any:
		return c
	}

	return nil
}

func hasWildcard(steps []step) bool {
	for _, st := range steps {
		if st.kind == stepWildcard {
			return true
		}
	}

	return false
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}

	return true
}

func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	}

	return ""
}

// jsonValue resolves a "$.path" reference against message, which need not
// be a selector the pattern itself names.
func jsonValue(message, ref string) (string, bool) {
	sel, end, err := lexSelector(ref, 0)
	if err != nil || end != len(ref) || hasWildcard(sel.steps) {
		return "", false
	}

	doc, ok := decodeJSON(message)
	if !ok {
		return "", false
	}

	vals := resolve(doc, sel.steps)
	if len(vals) != 1 || !isScalar(vals[0]) {
		return "", false
	}

	return scalarString(vals[0]), true
}
