package asl

import (
	"strconv"
	"strings"
)

// evalPath evaluates a JSONPath reference against root, returning the selected
// value and whether it was present. Only the reference/selection subset is
// supported: "$", "$.a.b", "$[0]", "$.a.b[2]". Filters, wildcards and recursive
// descent are rejected with an error so an unsupported path fails loudly rather
// than returning a wrong silent result.
func evalPath(path string, root any) (value any, present bool, err error) {
	if path == "" || path[0] != '$' {
		return nil, false, aslErrf("invalid JSONPath %q: must start with '$'", path)
	}

	if rerr := rejectUnsupportedPath(path); rerr != nil {
		return nil, false, rerr
	}

	if path == "$" {
		return root, true, nil
	}

	toks, terr := tokenizePath(path[1:])
	if terr != nil {
		return nil, false, terr
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

func rejectUnsupportedPath(path string) error {
	if strings.ContainsAny(path, "*?@") || strings.Contains(path, "..") {
		return aslErrf("JSONPath %q uses unsupported syntax (filters/wildcards/recursive descent)", path)
	}

	return nil
}

// pathToken is one selection step: a map field or a slice index.
type pathToken struct {
	field   string
	index   int
	isIndex bool
}

func (t pathToken) apply(cur any) (any, bool) {
	if t.isIndex {
		arr, ok := cur.([]any)
		if !ok || t.index < 0 || t.index >= len(arr) {
			return nil, false
		}

		return arr[t.index], true
	}

	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false
	}

	v, ok := m[t.field]

	return v, ok
}

// tokenizePath splits the portion of a path after the leading '$' into tokens.
func tokenizePath(s string) ([]pathToken, error) {
	var toks []pathToken

	for s != "" {
		switch s[0] {
		case '.':
			field, rest := scanField(s[1:])
			if field == "" {
				return nil, aslErrf("empty field name in JSONPath")
			}

			toks = append(toks, pathToken{field: field})
			s = rest
		case '[':
			tok, rest, err := scanBracket(s)
			if err != nil {
				return nil, err
			}

			toks = append(toks, tok)
			s = rest
		default:
			return nil, aslErrf("unexpected character %q in JSONPath", s[0])
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

// scanBracket reads a "[...]" selector: a numeric index, or a quoted field name.
func scanBracket(s string) (pathToken, string, error) {
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return pathToken{}, "", aslErrf("unterminated '[' in JSONPath")
	}

	inner := s[1:end]
	rest := s[end+1:]

	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') {
		return pathToken{field: inner[1 : len(inner)-1]}, rest, nil
	}

	idx, err := strconv.Atoi(inner)
	if err != nil {
		return pathToken{}, "", aslErrf("invalid array index %q in JSONPath", inner)
	}

	return pathToken{index: idx, isIndex: true}, rest, nil
}
