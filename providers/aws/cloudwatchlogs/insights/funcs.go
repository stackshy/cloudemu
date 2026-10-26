package insights

import (
	"math"
	"strings"
)

func arith(op string, a, b float64) any {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		if b == 0 {
			return nil
		}

		return a / b
	case "%":
		if b == 0 {
			return nil
		}

		return math.Mod(a, b)
	default:
		return math.Pow(a, b)
	}
}

// fn evaluates a function call over its argument values.
type fn func(args []any) any

type fnSpec struct {
	minArgs, maxArgs int // maxArgs < 0 means variadic
	call             fn
}

// functions is the supported subset of the general, string and numeric
// functions.
//
//nolint:gochecknoglobals // fixed function table.
var functions = map[string]fnSpec{
	"ispresent": {1, 1, func(a []any) any { return a[0] != nil }},
	"isempty":   {1, 1, func(a []any) any { return a[0] == nil || toStr(a[0]) == "" }},
	"isblank":   {1, 1, func(a []any) any { return a[0] == nil || strings.TrimSpace(toStr(a[0])) == "" }},
	"coalesce":  {1, -1, coalesce},
	"concat":    {1, -1, concat},
	"strlen":    {1, 1, strArg(func(s string) any { return float64(len([]rune(s))) })},
	"toupper":   {1, 1, strArg(func(s string) any { return strings.ToUpper(s) })},
	"tolower":   {1, 1, strArg(func(s string) any { return strings.ToLower(s) })},
	"trim":      {1, 1, strArg(func(s string) any { return strings.TrimSpace(s) })},
	"ltrim":     {1, 1, strArg(func(s string) any { return strings.TrimLeft(s, " \t") })},
	"rtrim":     {1, 1, strArg(func(s string) any { return strings.TrimRight(s, " \t") })},
	"strcontains": {2, 2, func(a []any) any {
		return a[0] != nil && strings.Contains(toStr(a[0]), toStr(a[1]))
	}},
	"replace": {3, 3, func(a []any) any {
		if a[0] == nil {
			return nil
		}

		return strings.ReplaceAll(toStr(a[0]), toStr(a[1]), toStr(a[2]))
	}},
	"substr":   {2, 3, substr},
	"abs":      {1, 1, numArg(math.Abs)},
	"ceil":     {1, 1, numArg(math.Ceil)},
	"floor":    {1, 1, numArg(math.Floor)},
	"round":    {1, 1, numArg(math.Round)},
	"sqrt":     {1, 1, numArg(math.Sqrt)},
	"log":      {1, 1, numArg(math.Log)},
	"tonumber": {1, 1, numArg(func(f float64) float64 { return f })},
	"greatest": {1, -1, extreme(1)},
	"least":    {1, -1, extreme(-1)},
}

func strArg(f func(string) any) fn {
	return func(a []any) any {
		if a[0] == nil {
			return nil
		}

		return f(toStr(a[0]))
	}
}

func numArg(f func(float64) float64) fn {
	return func(a []any) any {
		x, ok := toNum(a[0])
		if !ok {
			return nil
		}

		return f(x)
	}
}

func coalesce(a []any) any {
	for _, v := range a {
		if v != nil {
			return v
		}
	}

	return nil
}

func concat(a []any) any {
	var b strings.Builder

	for _, v := range a {
		b.WriteString(toStr(v))
	}

	return b.String()
}

func substr(a []any) any {
	if a[0] == nil {
		return nil
	}

	r := []rune(toStr(a[0]))

	f, ok := toNum(a[1])
	if !ok {
		return nil
	}

	start, ok := clampIndex(f, len(r))
	if !ok || f < 0 || f > float64(len(r)) {
		return nil
	}

	end := len(r)

	const lengthArg = 2

	if len(a) > lengthArg {
		n, nok := toNum(a[lengthArg])
		if !nok {
			return string(r[start:])
		}

		count, cok := clampIndex(n, len(r)-start)
		if !cok {
			return nil
		}

		end = start + count
	}

	return string(r[start:end])
}

// clampIndex converts f to an int in [0, maxIdx], truncating toward zero.
// It reports false for NaN and ±Inf, whose int conversion is
// implementation-defined (int(NaN) is MinInt64 on amd64).
func clampIndex(f float64, maxIdx int) (int, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}

	switch {
	case f <= 0:
		return 0, true
	case f >= float64(maxIdx):
		return maxIdx, true
	default:
		return int(f), true
	}
}

// extreme returns greatest (sign 1) or least (sign -1) of numeric arguments.
func extreme(sign float64) fn {
	return func(a []any) any {
		var (
			best  float64
			found bool
		)

		for _, v := range a {
			if x, ok := toNum(v); ok && (!found || sign*(x-best) > 0) {
				best, found = x, true
			}
		}

		if !found {
			return nil
		}

		return best
	}
}

type callExpr struct {
	spec fnSpec
	args []expr
}

func (c callExpr) eval(rec Record) any {
	vals := make([]any, len(c.args))
	for i, a := range c.args {
		vals[i] = a.eval(rec)
	}

	return c.spec.call(vals)
}
