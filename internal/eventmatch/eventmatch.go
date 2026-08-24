// Package eventmatch implements the content-filtering semantics shared by
// EventBridge event patterns and SNS subscription filter policies. Both use the
// same leaf model: a field constraint is a JSON array whose elements are either
// plain values (exact match) or single-key operator objects (prefix, suffix,
// anything-but, exists, numeric, cidr, equals-ignore-case, wildcard). Provider
// mocks own their storage and wiring; this package is only the matcher.
package eventmatch

import (
	"encoding/json"
	"math"
	"net"
	"strings"
)

// MatchEvent reports whether the parsed event object satisfies the parsed
// pattern object. Every key in the pattern must be present in the event and
// match: a pattern value that is a nested object recurses into the event's
// nested object; a pattern value that is an array is a leaf constraint. Any
// other pattern shape is treated as non-matching.
func MatchEvent(pattern, event map[string]any) bool {
	for key, pv := range pattern {
		ev, present := event[key]

		switch p := pv.(type) {
		case []any:
			if !MatchLeaf(p, ev, present) {
				return false
			}
		case map[string]any:
			child, ok := ev.(map[string]any)
			if !ok || !MatchEvent(p, child) {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// MatchLeaf reports whether a concrete event value (present or absent) satisfies
// at least one entry of a leaf constraint array. When the event value is itself
// an array, the constraint matches if any element matches.
func MatchLeaf(allowed []any, value any, present bool) bool {
	// When the event value is an array, the leaf matches if any element does;
	// otherwise fall through so exists-style operators can evaluate presence.
	if arr, ok := value.([]any); ok && present {
		for _, el := range arr {
			if matchAnyEntry(allowed, el, true) {
				return true
			}
		}
	}

	return matchAnyEntry(allowed, value, present)
}

func matchAnyEntry(allowed []any, value any, present bool) bool {
	for _, a := range allowed {
		if matchEntry(a, value, present) {
			return true
		}
	}

	return false
}

func matchEntry(allowed, value any, present bool) bool {
	switch a := allowed.(type) {
	case map[string]any:
		return matchOperator(a, value, present)
	case nil:
		return present && value == nil
	default:
		return present && equalScalar(a, value)
	}
}

func matchOperator(op map[string]any, value any, present bool) bool {
	for name, spec := range op {
		if !matchNamedOperator(name, spec, value, present) {
			return false
		}
	}

	return true
}

func matchNamedOperator(name string, spec, value any, present bool) bool {
	// exists is the only operator that evaluates the absent case; every other
	// operator requires a present value.
	if name == "exists" {
		want, _ := spec.(bool)

		return want == present
	}

	return present && matchPresentOperator(name, spec, value)
}

func matchPresentOperator(name string, spec, value any) bool {
	switch name {
	case "prefix":
		return matchAffix(spec, value, strings.HasPrefix)
	case "suffix":
		return matchAffix(spec, value, strings.HasSuffix)
	case "equals-ignore-case":
		return matchEqualsIgnoreCase(spec, value)
	case "anything-but":
		return matchAnythingBut(spec, value)
	case "numeric":
		return matchNumeric(spec, value)
	case "cidr":
		return matchCIDR(spec, value)
	case "wildcard":
		return matchWildcard(spec, value)
	default:
		return false
	}
}

func matchAffix(spec, value any, test func(s, affix string) bool) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}

	switch p := spec.(type) {
	case string:
		return test(s, p)
	case map[string]any:
		if ic, ok := p["equals-ignore-case"].(string); ok {
			return test(strings.ToLower(s), strings.ToLower(ic))
		}
	}

	return false
}

func matchEqualsIgnoreCase(spec, value any) bool {
	want, ok := spec.(string)
	if !ok {
		return false
	}

	got, ok := value.(string)

	return ok && strings.EqualFold(got, want)
}

func matchAnythingBut(spec, value any) bool {
	switch s := spec.(type) {
	case []any:
		for _, e := range s {
			if equalScalar(e, value) {
				return false
			}
		}

		return true
	case map[string]any:
		return !matchOperator(s, value, true)
	default:
		return !equalScalar(spec, value)
	}
}

func matchNumeric(spec, value any) bool {
	v, ok := toFloat(value)
	if !ok {
		return false
	}

	arr, ok := spec.([]any)
	if !ok {
		return false
	}

	for i := 0; i+1 < len(arr); i += 2 {
		opStr, ok := arr[i].(string)
		if !ok {
			return false
		}

		bound, ok := toFloat(arr[i+1])
		if !ok || !numCompare(v, opStr, bound) {
			return false
		}
	}

	return true
}

func numCompare(v float64, op string, bound float64) bool {
	const eps = 1e-9

	switch op {
	case "=":
		return math.Abs(v-bound) < eps
	case "!=":
		return math.Abs(v-bound) >= eps
	case "<":
		return v < bound
	case "<=":
		return v <= bound
	case ">":
		return v > bound
	case ">=":
		return v >= bound
	default:
		return false
	}
}

func matchCIDR(spec, value any) bool {
	cidr, ok := spec.(string)
	if !ok {
		return false
	}

	ipStr, ok := value.(string)
	if !ok {
		return false
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(ipStr)

	return ip != nil && network.Contains(ip)
}

func matchWildcard(spec, value any) bool {
	pattern, ok := spec.(string)
	if !ok {
		return false
	}

	s, ok := value.(string)
	if !ok {
		return false
	}

	return wildcardMatch(pattern, s)
}

// wildcardMatch reports whether s matches pattern, where '*' matches any run of
// characters. Segments between '*' must appear in order.
func wildcardMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}

	if !strings.HasPrefix(s, parts[0]) {
		return false
	}

	rest := s[len(parts[0]):]

	for _, mid := range parts[1 : len(parts)-1] {
		idx := strings.Index(rest, mid)
		if idx < 0 {
			return false
		}

		rest = rest[idx+len(mid):]
	}

	return strings.HasSuffix(rest, parts[len(parts)-1])
}

func equalScalar(a, value any) bool {
	if an, ok := toFloat(a); ok {
		if vn, ok := toFloat(value); ok {
			return an == vn
		}

		return false
	}

	return a == value
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()

		return f, err == nil
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// ParsePattern decodes a JSON event pattern / filter policy into the object form
// MatchEvent and MatchLeaf consume. It returns false when the JSON is not a
// well-formed object.
func ParsePattern(raw string) (map[string]any, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}

	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false
	}

	return p, true
}
