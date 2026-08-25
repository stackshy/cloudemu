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
	"strconv"
	"strings"
)

// Leaf operator names shared by the matcher and reserved-keyword checks.
const (
	opPrefix           = "prefix"
	opSuffix           = "suffix"
	opAnythingBut      = "anything-but"
	opExists           = "exists"
	opNumeric          = "numeric"
	opCIDR             = "cidr"
	opEqualsIgnoreCase = "equals-ignore-case"
	opWildcard         = "wildcard"
)

// MatchEvent reports whether the parsed event object satisfies the parsed
// pattern object. Every key in the pattern must be present in the event and
// match: a pattern value that is a nested object recurses into the event's
// nested object; a pattern value that is an array is a leaf constraint. Any
// other pattern shape is treated as non-matching.
func MatchEvent(pattern, event map[string]any) bool {
	for key, pv := range pattern {
		if !matchPatternKey(key, pv, event) {
			return false
		}
	}

	return true
}

func matchPatternKey(key string, pv any, event map[string]any) bool {
	if key == "$or" {
		if subs, ok := pv.([]any); ok && IsOrClause(subs) {
			return matchOrClause(subs, event)
		}
	}

	ev, present := event[key]

	switch p := pv.(type) {
	case []any:
		return MatchLeaf(p, ev, present)
	case map[string]any:
		return matchNestedObject(p, ev)
	default:
		return false
	}
}

// matchNestedObject applies a nested-object pattern to an event value. It matches
// an object directly, and matches a JSON array if the pattern matches any element
// — mirroring MatchLeaf's array handling. This makes a nested filter policy like
// the documented S3->SNS body filter {"Records":{"s3":{"object":{"key":[...]}}}}
// match an S3 event whose top-level "Records" is an array of objects.
func matchNestedObject(pattern map[string]any, ev any) bool {
	switch child := ev.(type) {
	case map[string]any:
		return MatchEvent(pattern, child)
	case []any:
		for _, el := range child {
			if matchNestedObject(pattern, el) {
				return true
			}
		}

		return false
	default:
		return false
	}
}

// IsOrClause reports whether a "$or" value is a genuine logical-OR clause: a list
// of at least two sub-pattern objects, none of which name a reserved leaf
// operator as a field. AWS documents `$or` as a first-class operator whose value
// is an array of independent sub-patterns, matched if any one of them matches.
func IsOrClause(subs []any) bool {
	const minOrBranches = 2
	if len(subs) < minOrBranches {
		return false
	}

	for _, s := range subs {
		sub, ok := s.(map[string]any)
		if !ok {
			return false
		}

		for k := range sub {
			if isReservedOperator(k) {
				return false
			}
		}
	}

	return true
}

func matchOrClause(subs []any, event map[string]any) bool {
	for _, s := range subs {
		if sub, ok := s.(map[string]any); ok && MatchEvent(sub, event) {
			return true
		}
	}

	return false
}

func isReservedOperator(name string) bool {
	switch name {
	case opPrefix, opSuffix, opAnythingBut, opExists,
		opNumeric, opCIDR, opEqualsIgnoreCase, opWildcard:
		return true
	default:
		return false
	}
}

// MatchLeaf reports whether a concrete event value (present or absent) satisfies
// at least one entry of a leaf constraint array. When the event value is itself
// an array, the constraint matches if any element matches.
func MatchLeaf(allowed []any, value any, present bool) bool {
	return matchLeaf(allowed, value, present, false)
}

// MatchLeafAttr is MatchLeaf for SNS message-attribute matching, where values
// arrive as strings even when their DataType is Number. It lets a numeric
// operator parse a numeric string (so a "150" attribute satisfies {"numeric":
// [">",100]}), while body/EventBridge matching via MatchLeaf keeps the stricter
// rule that a JSON string never satisfies a numeric operator.
func MatchLeafAttr(allowed []any, value any, present bool) bool {
	return matchLeaf(allowed, value, present, true)
}

func matchLeaf(allowed []any, value any, present, coerceNum bool) bool {
	// When the event value is an array, the leaf matches if any element does;
	// otherwise fall through so exists-style operators can evaluate presence.
	if arr, ok := value.([]any); ok && present {
		for _, el := range arr {
			if matchAnyEntry(allowed, el, true, coerceNum) {
				return true
			}
		}
	}

	return matchAnyEntry(allowed, value, present, coerceNum)
}

func matchAnyEntry(allowed []any, value any, present, coerceNum bool) bool {
	for _, a := range allowed {
		if matchEntry(a, value, present, coerceNum) {
			return true
		}
	}

	return false
}

func matchEntry(allowed, value any, present, coerceNum bool) bool {
	switch a := allowed.(type) {
	case map[string]any:
		return matchOperator(a, value, present, coerceNum)
	case nil:
		return present && value == nil
	default:
		return present && equalScalar(a, value)
	}
}

func matchOperator(op map[string]any, value any, present, coerceNum bool) bool {
	for name, spec := range op {
		if !matchNamedOperator(name, spec, value, present, coerceNum) {
			return false
		}
	}

	return true
}

func matchNamedOperator(name string, spec, value any, present, coerceNum bool) bool {
	// exists is the only operator that evaluates the absent case; every other
	// operator requires a present value.
	if name == opExists {
		want, _ := spec.(bool)

		return want == present
	}

	return present && matchPresentOperator(name, spec, value, coerceNum)
}

func matchPresentOperator(name string, spec, value any, coerceNum bool) bool {
	switch name {
	case opPrefix:
		return matchAffix(spec, value, strings.HasPrefix)
	case opSuffix:
		return matchAffix(spec, value, strings.HasSuffix)
	case opEqualsIgnoreCase:
		return matchEqualsIgnoreCase(spec, value)
	case opAnythingBut:
		return matchAnythingBut(spec, value, coerceNum)
	case opNumeric:
		return matchNumeric(spec, value, coerceNum)
	case opCIDR:
		return matchCIDR(spec, value)
	case opWildcard:
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
	case []any:
		for _, e := range p {
			if matchAffix(e, value, test) {
				return true
			}
		}
	case map[string]any:
		if ic, ok := p[opEqualsIgnoreCase].(string); ok {
			return test(strings.ToLower(s), strings.ToLower(ic))
		}
	}

	return false
}

func matchEqualsIgnoreCase(spec, value any) bool {
	if list, ok := spec.([]any); ok {
		for _, e := range list {
			if matchEqualsIgnoreCase(e, value) {
				return true
			}
		}

		return false
	}

	want, ok := spec.(string)
	if !ok {
		return false
	}

	got, ok := value.(string)

	return ok && strings.EqualFold(got, want)
}

func matchAnythingBut(spec, value any, coerceNum bool) bool {
	switch s := spec.(type) {
	case []any:
		for _, e := range s {
			if equalScalar(e, value) {
				return false
			}
		}

		return true
	case map[string]any:
		return !matchOperator(s, value, true, coerceNum)
	default:
		return !equalScalar(spec, value)
	}
}

func matchNumeric(spec, value any, coerceNum bool) bool {
	v, ok := numericValue(value, coerceNum)
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
	if list, ok := spec.([]any); ok {
		for _, e := range list {
			if matchWildcard(e, value) {
				return true
			}
		}

		return false
	}

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

// numericValue resolves an event value to a float for numeric comparison. With
// coerceNum set (SNS message-attribute scope), a numeric string is parsed, since
// attribute values travel the wire as strings; a non-numeric string still fails.
// Without it (body/EventBridge scope), a JSON string never satisfies a numeric
// operator, so string values are not coerced.
func numericValue(value any, coerceNum bool) (float64, bool) {
	if coerceNum {
		if s, ok := value.(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)

			return f, err == nil
		}
	}

	return toFloat(value)
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
