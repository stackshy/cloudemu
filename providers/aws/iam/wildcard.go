package iam

import "strings"

// arnSegments is the number of colon-separated components in an ARN:
// arn:partition:service:region:account:resource. The resource component may
// itself contain colons.
const arnSegments = 6

// globMatch reports whether value matches pattern under IAM's wildcard rules:
// '*' matches any run of characters (including none), '?' matches exactly one,
// and everything else is literal. The whole value must match, so the pattern is
// anchored at both ends. The comparison is case-sensitive.
func globMatch(pattern, value string) bool {
	p, v := []rune(pattern), []rune(value)
	pi, vi := 0, 0
	star, mark := -1, 0

	for vi < len(v) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, vi
			pi++
		case pi < len(p) && (p[pi] == '?' || p[pi] == v[vi]):
			pi++
			vi++
		case star >= 0:
			// Let the last '*' take one more character and retry from there.
			mark++
			pi, vi = star+1, mark
		default:
			return false
		}
	}

	for pi < len(p) && p[pi] == '*' {
		pi++
	}

	return pi == len(p)
}

// actionMatch matches an Action or NotAction entry against an action name.
// Action names are case-insensitive in IAM.
func actionMatch(pattern, action string) bool {
	return globMatch(strings.ToLower(pattern), strings.ToLower(action))
}

// arnMatch implements ArnEquals and ArnLike: each of the six ARN components is
// matched on its own, so a wildcard never spans the ':' between components (the
// last component keeps any colons it carries). Matching is case-sensitive. A
// pattern with no ':' at all, such as "*", is matched against the whole value.
func arnMatch(value, pattern string) bool {
	if !strings.Contains(pattern, ":") {
		return globMatch(pattern, value)
	}

	pp := strings.SplitN(pattern, ":", arnSegments)
	vp := strings.SplitN(value, ":", arnSegments)

	if len(pp) != arnSegments || len(vp) != arnSegments {
		return false
	}

	for i := range pp {
		if !globMatch(pp[i], vp[i]) {
			return false
		}
	}

	return true
}
