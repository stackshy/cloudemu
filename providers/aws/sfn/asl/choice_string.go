package asl

import "strings"

func isStringOp(op string) bool {
	if op == opStringMatches {
		return true
	}

	base := strings.TrimSuffix(op, "Path")

	return inList(base, "StringEquals", "StringLessThan", "StringGreaterThan",
		"StringLessThanEquals", "StringGreaterThanEquals")
}

// evalString evaluates the string comparator family. StringMatches applies a
// glob-style pattern; the others are lexicographic comparisons. A type mismatch
// is a non-match.
func evalString(op string, val, comparand any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}

	c, ok := comparand.(string)
	if !ok {
		return false
	}

	if op == opStringMatches {
		return globMatch(c, s)
	}

	return orderMatch(strings.Compare(s, c), op)
}

// globMatch reports whether s matches an ASL StringMatches pattern, where '*'
// matches any (possibly empty) sequence and '\' escapes the next character. It
// works by splitting the pattern into the literal segments between stars and
// matching them in order.
func globMatch(pattern, s string) bool {
	parts := splitOnStar(pattern)
	if len(parts) == 1 {
		return parts[0] == s
	}

	if !strings.HasPrefix(s, parts[0]) {
		return false
	}

	rem := s[len(parts[0]):]
	last := len(parts) - 1

	for i := 1; i < last; i++ {
		idx := strings.Index(rem, parts[i])
		if idx < 0 {
			return false
		}

		rem = rem[idx+len(parts[i]):]
	}

	return strings.HasSuffix(rem, parts[last])
}

// splitOnStar splits a pattern into the literal segments delimited by unescaped
// '*' characters, honoring '\' escapes.
func splitOnStar(pattern string) []string {
	var (
		parts []string
		cur   strings.Builder
	)

	for i := 0; i < len(pattern); i++ {
		c := pattern[i]

		switch {
		case c == '\\' && i+1 < len(pattern):
			i++
			cur.WriteByte(pattern[i])
		case c == '*':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}

	return append(parts, cur.String())
}
