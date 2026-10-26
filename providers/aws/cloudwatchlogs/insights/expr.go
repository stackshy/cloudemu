package insights

import (
	"regexp"
	"strconv"
	"strings"
)

// Record is one log event's fields: strings, float64 numbers, bools, and
// nested JSON already flattened to dotted names. A missing field is absent.
type Record map[string]any

type expr interface {
	eval(rec Record) any
}

type fieldRef struct{ name string }

func (f fieldRef) eval(rec Record) any { return rec[f.name] }

type literal struct{ v any }

func (l literal) eval(Record) any { return l.v }

type regexLit struct{ re *regexp.Regexp }

func (r regexLit) eval(Record) any { return r.re }

type notExpr struct{ x expr }

func (n notExpr) eval(rec Record) any { return !truthy(n.x.eval(rec)) }

type negExpr struct{ x expr }

func (n negExpr) eval(rec Record) any {
	f, ok := toNum(n.x.eval(rec))
	if !ok {
		return nil
	}

	return -f
}

type logicExpr struct {
	and  bool
	l, r expr
}

func (e logicExpr) eval(rec Record) any {
	if e.and {
		return truthy(e.l.eval(rec)) && truthy(e.r.eval(rec))
	}

	return truthy(e.l.eval(rec)) || truthy(e.r.eval(rec))
}

// cmpExpr compares two values. Both sides must be present; numbers (and
// numeric strings) compare numerically, anything else as strings.
type cmpExpr struct {
	op   string
	l, r expr
}

func (e cmpExpr) eval(rec Record) any {
	a, b := e.l.eval(rec), e.r.eval(rec)
	if a == nil || b == nil {
		return false
	}

	if e.op == opMatch || e.op == opNotMatch {
		return regexMatch(a, b) == (e.op == opMatch)
	}

	c := compare(a, b)

	switch e.op {
	case opEq:
		return c == 0
	case opNe:
		return c != 0
	case opLt:
		return c < 0
	case opLe:
		return c <= 0
	case opGt:
		return c > 0
	default:
		return c >= 0
	}
}

type arithExpr struct {
	op   string
	l, r expr
}

func (e arithExpr) eval(rec Record) any {
	a, aok := toNum(e.l.eval(rec))
	b, bok := toNum(e.r.eval(rec))

	if !aok || !bok {
		return nil
	}

	return arith(e.op, a, b)
}

// likeExpr is `x like "text"` (a case-sensitive substring match) or
// `x like /regex/`, optionally negated with `not like`.
type likeExpr struct {
	x, pattern expr
	negate     bool
}

func (e likeExpr) eval(rec Record) any {
	v := e.x.eval(rec)
	if v == nil {
		return false
	}

	var matched bool
	if re, ok := e.pattern.eval(rec).(*regexp.Regexp); ok {
		matched = re.MatchString(toStr(v))
	} else {
		matched = strings.Contains(toStr(v), toStr(e.pattern.eval(rec)))
	}

	return matched != e.negate
}

type inExpr struct {
	x      expr
	list   []expr
	negate bool
}

func (e inExpr) eval(rec Record) any {
	v := e.x.eval(rec)
	if v == nil {
		return false
	}

	for _, item := range e.list {
		if iv := item.eval(rec); iv != nil && compare(v, iv) == 0 {
			return !e.negate
		}
	}

	return e.negate
}

func regexMatch(v, pattern any) bool {
	if re, ok := pattern.(*regexp.Regexp); ok {
		return re.MatchString(toStr(v))
	}

	re, err := regexp.Compile(toStr(pattern))

	return err == nil && re.MatchString(toStr(v))
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}

	return false
}

func toNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)

		return f, err == nil
	case bool:
		if x {
			return 1, true
		}

		return 0, true
	}

	return 0, false
}

// toStr renders a value the way query results show it.
func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "1"
		}

		return "0"
	case *regexp.Regexp:
		return x.String()
	}

	return ""
}

// compare orders two present values: numerically when both are numbers,
// otherwise as strings.
func compare(a, b any) int {
	if x, ok := toNum(a); ok {
		if y, ok2 := toNum(b); ok2 {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			default:
				return 0
			}
		}
	}

	return strings.Compare(toStr(a), toStr(b))
}
