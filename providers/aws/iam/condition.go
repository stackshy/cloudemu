package iam

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// ConditionContext carries the request condition keys available for policy
// evaluation (e.g. "aws:SourceIp", "aws:CurrentTime", "aws:SecureTransport",
// "aws:PrincipalArn", "aws:RequestedRegion"). AWS condition keys are matched
// case-insensitively, so lookups normalize the key. A nil or empty context means
// no keys are available: a plain condition referencing a key then fails to
// match, while its ...IfExists variant passes (real IAM rules).
type ConditionContext map[string]string

// get resolves a condition key case-insensitively, reporting whether it was
// present in the request context.
func (c ConditionContext) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}

	if v, ok := c[key]; ok {
		return v, true
	}

	for k, v := range c {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}

	return "", false
}

// evaluateConditions reports whether every operator/key in a statement's
// Condition block is satisfied against the request context. AWS combines them
// with AND across operators and AND across keys within an operator; within a
// single key the listed values combine with OR (a negative operator requires
// that none match). An empty condition block is vacuously true.
func evaluateConditions(conds map[string]map[string]any, cctx ConditionContext) bool {
	return evaluateConditionsWith(conds, cctx, absentKeyIAM)
}

// absentKey says how a condition key missing from the request context is
// treated.
type absentKey int

const (
	// absentKeyIAM is real IAM: a plain operator fails, its ...IfExists form
	// passes, and Null tests for presence.
	absentKeyIAM absentKey = iota
	// absentKeyMatches treats every missing key as satisfied, whatever the
	// operator. Used for Deny statements when the resource is unknown.
	absentKeyMatches
	// absentKeyFails treats every missing key as unsatisfied, whatever the
	// operator. Used for Allow statements when the resource is unknown.
	absentKeyFails
)

// evaluateConditionsWith is evaluateConditions with an explicit rule for keys
// the request context does not carry.
func evaluateConditionsWith(conds map[string]map[string]any, cctx ConditionContext, absent absentKey) bool {
	for rawOp, keyVals := range conds {
		for key, raw := range keyVals {
			if !evaluateConditionKey(rawOp, key, toStringSlice(raw), cctx, absent) {
				return false
			}
		}
	}

	return true
}

// evaluateConditionKey evaluates one operator against one condition key and its
// policy-supplied values. It resolves the request value from the context,
// applies the ...IfExists rule for an absent key, and dispatches to the operator
// family. The Null operator is handled first because it is defined in terms of
// key presence, not the key's value. A non-IAM absent rule overrides all of
// that for a missing key.
func evaluateConditionKey(rawOp, key string, values []string, cctx ConditionContext, absent absentKey) bool {
	set, op := splitSetOperator(rawOp)
	base, ifExists := splitIfExists(op)

	ctxVal, present := cctx.get(key)

	if !present && absent != absentKeyIAM {
		return absent == absentKeyMatches
	}

	if strings.EqualFold(base, "Null") {
		return evalNull(present, values)
	}

	if !present {
		// ForAllValues is vacuously true for a missing key, ForAnyValue is false.
		if set != "" {
			return set == setForAll
		}

		// A missing key fails a positive condition; the ...IfExists variant
		// passes so the statement is gated only when the key is actually
		// supplied. A negated operator (StringNotEquals, ArnNotLike, ...) is true
		// when the key is missing: nothing in the request equals the value (IAM
		// User Guide, "IAM JSON policy elements: Condition operators").
		return ifExists || negatedOperator(base)
	}

	if set != "" {
		return evalSetOperator(set, base, ctxVal, values)
	}

	return evalPresentOperator(base, ctxVal, values)
}

// negatedOperator reports whether op is a negated condition operator, which
// matches when the request carries no value equal to (or like) the policy's.
func negatedOperator(op string) bool {
	if _, negate, ok := comparatorFor(op); ok {
		return negate
	}

	if _, negate, ok := numericRelation(op); ok {
		return negate
	}

	_, negate, ok := dateRelation(op)

	return ok && negate
}

// The set-operator qualifiers for multivalued condition keys.
const (
	setForAll = "ForAllValues"
	setForAny = "ForAnyValue"
)

// splitSetOperator strips a "ForAllValues:" or "ForAnyValue:" qualifier from
// an operator, returning the qualifier ("" when there is none) and the rest.
func splitSetOperator(op string) (set, rest string) {
	for _, q := range []string{setForAll, setForAny} {
		if after, ok := strings.CutPrefix(op, q+":"); ok {
			return q, after
		}
	}

	return "", op
}

// evalSetOperator applies op to each value of a multivalued key, whose values
// are joined by driver.ConditionValueSeparator. ForAllValues needs every
// request value to satisfy op, ForAnyValue at least one.
func evalSetOperator(set, op, ctxVal string, values []string) bool {
	for _, v := range strings.Split(ctxVal, driver.ConditionValueSeparator) {
		ok := evalPresentOperator(op, v, values)
		if set == setForAny && ok {
			return true
		}

		if set == setForAll && !ok {
			return false
		}
	}

	return set == setForAll
}

// evalPresentOperator evaluates an operator whose key is present. String-shaped
// operators (String*, Arn*, Ip*, Bool) share the value-comparator path; the
// numeric and date families parse their operands. An unrecognized operator
// conservatively fails to match.
func evalPresentOperator(op, ctxVal string, values []string) bool {
	if cmp, negate, ok := comparatorFor(op); ok {
		return matchAny(values, negate, func(policyVal string) bool { return cmp(ctxVal, policyVal) })
	}

	if result, ok := evalNumeric(op, ctxVal, values); ok {
		return result
	}

	if result, ok := evalDate(op, ctxVal, values); ok {
		return result
	}

	return false
}

// splitIfExists strips a trailing "IfExists" suffix from an operator name,
// reporting whether it was present.
func splitIfExists(op string) (base string, ifExists bool) {
	const suffix = "IfExists"
	if strings.HasSuffix(op, suffix) && len(op) > len(suffix) {
		return op[:len(op)-len(suffix)], true
	}

	return op, false
}

// matchAny applies pred to each policy value. For a positive operator it reports
// whether any value matches; for a negative operator (negate) it reports whether
// none match.
func matchAny(values []string, negate bool, pred func(policyVal string) bool) bool {
	for _, v := range values {
		if pred(v) {
			return !negate
		}
	}

	return negate
}

// comparatorFor returns the string-value comparator for op (comparing the
// request value to a policy value), whether the operator is negative, and
// whether op belongs to a string-shaped family.
func comparatorFor(op string) (cmp func(ctxVal, policyVal string) bool, negate, ok bool) {
	if cmp, negate, ok := stringOp(op); ok {
		return cmp, negate, ok
	}

	if cmp, negate, ok := arnOp(op); ok {
		return cmp, negate, ok
	}

	if cmp, negate, ok := ipOp(op); ok {
		return cmp, negate, ok
	}

	if op == "Bool" {
		return boolMatch, false, true
	}

	return nil, false, false
}

func stringOp(op string) (cmp func(ctxVal, policyVal string) bool, negate, ok bool) {
	switch op {
	case "StringEquals":
		return strEqual, false, true
	case "StringNotEquals":
		return strEqual, true, true
	case "StringEqualsIgnoreCase":
		return strings.EqualFold, false, true
	case "StringNotEqualsIgnoreCase":
		return strings.EqualFold, true, true
	case "StringLike":
		return strLike, false, true
	case "StringNotLike":
		return strLike, true, true
	default:
		return nil, false, false
	}
}

// arnOp handles ARN operators. ArnEquals and ArnLike behave the same in IAM:
// both match component by component and allow '*' and '?' in each component.
func arnOp(op string) (cmp func(ctxVal, policyVal string) bool, negate, ok bool) {
	switch op {
	case "ArnEquals", "ArnLike":
		return arnMatch, false, true
	case "ArnNotEquals", "ArnNotLike":
		return arnMatch, true, true
	default:
		return nil, false, false
	}
}

func ipOp(op string) (cmp func(ctxVal, policyVal string) bool, negate, ok bool) {
	switch op {
	case "IpAddress":
		return ipMatch, false, true
	case "NotIpAddress":
		return ipMatch, true, true
	default:
		return nil, false, false
	}
}

func strEqual(ctxVal, policyVal string) bool { return ctxVal == policyVal }

// strLike matches ctxVal against a policy pattern that may contain '*' and '?'
// wildcards, case-sensitively and against the whole value.
func strLike(ctxVal, policyVal string) bool { return globMatch(policyVal, ctxVal) }

// boolMatch compares the request and policy values as booleans.
func boolMatch(ctxVal, policyVal string) bool {
	return strings.EqualFold(ctxVal, "true") == strings.EqualFold(policyVal, "true")
}

// ipMatch reports whether the request IP falls within the policy CIDR block (or
// equals the policy bare address).
func ipMatch(ctxVal, policyVal string) bool {
	ip := net.ParseIP(strings.TrimSpace(ctxVal))
	if ip == nil {
		return false
	}

	return ipInCIDR(ip, strings.TrimSpace(policyVal))
}

// ipInCIDR reports whether ip falls within the CIDR block cidr. A bare address
// (no "/") is treated as a single-host match.
func ipInCIDR(ip net.IP, cidr string) bool {
	if !strings.Contains(cidr, "/") {
		other := net.ParseIP(cidr)
		return other != nil && ip.Equal(other)
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	return network.Contains(ip)
}

// evalNumeric evaluates a numeric operator. It reports handled=false when op is
// not a numeric operator, so the dispatcher can try the next family.
func evalNumeric(op, ctxVal string, values []string) (result, handled bool) {
	rel, negate, ok := numericRelation(op)
	if !ok {
		return false, false
	}

	cf, err := strconv.ParseFloat(strings.TrimSpace(ctxVal), 64)
	if err != nil {
		return false, true
	}

	return matchAny(values, negate, func(policyVal string) bool {
		vf, perr := strconv.ParseFloat(strings.TrimSpace(policyVal), 64)
		return perr == nil && rel(cf, vf)
	}), true
}

// numericRelation maps a numeric operator to its comparison (equality is used
// for both NumericEquals and NumericNotEquals; the latter negates the match).
func numericRelation(op string) (rel func(c, v float64) bool, negate, ok bool) {
	switch op {
	case "NumericEquals":
		return func(c, v float64) bool { return c == v }, false, true
	case "NumericNotEquals":
		return func(c, v float64) bool { return c == v }, true, true
	case "NumericLessThan":
		return func(c, v float64) bool { return c < v }, false, true
	case "NumericLessThanEquals":
		return func(c, v float64) bool { return c <= v }, false, true
	case "NumericGreaterThan":
		return func(c, v float64) bool { return c > v }, false, true
	case "NumericGreaterThanEquals":
		return func(c, v float64) bool { return c >= v }, false, true
	default:
		return nil, false, false
	}
}

// evalDate evaluates a date operator. It reports handled=false when op is not a
// date operator.
func evalDate(op, ctxVal string, values []string) (result, handled bool) {
	rel, negate, ok := dateRelation(op)
	if !ok {
		return false, false
	}

	ct, err := parseDate(ctxVal)
	if err != nil {
		return false, true
	}

	return matchAny(values, negate, func(policyVal string) bool {
		vt, perr := parseDate(policyVal)
		return perr == nil && rel(ct, vt)
	}), true
}

// dateRelation maps a date operator to its comparison (equality is used for both
// DateEquals and DateNotEquals; the latter negates the match).
func dateRelation(op string) (rel func(c, v time.Time) bool, negate, ok bool) {
	switch op {
	case "DateEquals":
		return func(c, v time.Time) bool { return c.Equal(v) }, false, true
	case "DateNotEquals":
		return func(c, v time.Time) bool { return c.Equal(v) }, true, true
	case "DateLessThan":
		return func(c, v time.Time) bool { return c.Before(v) }, false, true
	case "DateLessThanEquals":
		return func(c, v time.Time) bool { return !c.After(v) }, false, true
	case "DateGreaterThan":
		return func(c, v time.Time) bool { return c.After(v) }, false, true
	case "DateGreaterThanEquals":
		return func(c, v time.Time) bool { return !c.Before(v) }, false, true
	default:
		return nil, false, false
	}
}

// parseDate accepts an ISO 8601 / RFC 3339 timestamp or an epoch-seconds value,
// matching the forms AWS accepts for date condition keys and values.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC(), nil
	}

	return time.Parse("2006-01-02T15:04:05Z", s)
}

// evalNull implements the Null operator: a value of "true" requires the key to
// be absent from the request, "false" requires it to be present. Multiple values
// combine with OR.
func evalNull(present bool, values []string) bool {
	for _, v := range values {
		wantAbsent := strings.EqualFold(strings.TrimSpace(v), "true")
		if wantAbsent == !present {
			return true
		}
	}

	return false
}
