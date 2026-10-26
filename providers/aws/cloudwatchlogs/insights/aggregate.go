package insights

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// aggCall is one aggregation function call inside a stats command. Its value
// is computed once per group and read back from the group's slot record.
type aggCall struct {
	fn  aggFunc
	arg expr // nil for count(*) and count()
	pct float64
	key string
}

func (a *aggCall) eval(rec Record) any { return rec[a.key] }

type aggFunc func(a *aggCall, recs []Record) any

// Aggregation and stats-only functions.
const (
	aggCount = "count"
	aggPct   = "pct"
)

// maxPercent is the largest percentile pct accepts.
const maxPercent = 100

// aggregations are the stats functions, keyed by lower-case name.
// count_distinct is the general-functions spelling of countDistinct.
//
//nolint:gochecknoglobals // fixed function table.
var aggregations = map[string]aggFunc{
	aggCount:         aggCountFn,
	"count_distinct": aggCountDistinct,
	"countdistinct":  aggCountDistinct,
	"sum":            numericAgg(sum),
	"avg":            numericAgg(func(v []float64) float64 { return sum(v) / float64(len(v)) }),
	"stddev":         numericAgg(stddev),
	unitMin:          pickAgg(func(a, b any) bool { return compare(a, b) < 0 }),
	"max":            pickAgg(func(a, b any) bool { return compare(a, b) > 0 }),
	"sortsfirst":     pickAgg(func(a, b any) bool { return sortCompare(a, b) < 0 }),
	"sortslast":      pickAgg(func(a, b any) bool { return sortCompare(a, b) > 0 }),
	"earliest":       timeAgg(-1),
	"latest":         timeAgg(1),
	aggPct:           aggPercentile,
}

func aggCountFn(a *aggCall, recs []Record) any {
	if a.arg == nil {
		return float64(len(recs))
	}

	n := 0

	for _, r := range recs {
		if a.arg.eval(r) != nil {
			n++
		}
	}

	return float64(n)
}

func aggCountDistinct(a *aggCall, recs []Record) any {
	seen := map[string]bool{}

	for _, r := range recs {
		if v := a.arg.eval(r); v != nil {
			seen[toStr(v)] = true
		}
	}

	return float64(len(seen))
}

// numbers returns the argument's numeric values across recs.
func numbers(a *aggCall, recs []Record) []float64 {
	var out []float64

	for _, r := range recs {
		if f, ok := toNum(a.arg.eval(r)); ok {
			out = append(out, f)
		}
	}

	return out
}

// numericAgg folds the numeric values; with none, the result is absent.
func numericAgg(f func([]float64) float64) aggFunc {
	return func(a *aggCall, recs []Record) any {
		v := numbers(a, recs)
		if len(v) == 0 {
			return nil
		}

		return f(v)
	}
}

func sum(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}

	return s
}

// stddev is the population standard deviation, the square root of what
// variance() reports.
func stddev(v []float64) float64 {
	mean := sum(v) / float64(len(v))

	var sq float64
	for _, x := range v {
		sq += (x - mean) * (x - mean)
	}

	return math.Sqrt(sq / float64(len(v)))
}

// pickAgg keeps the present value that better ranks first.
func pickAgg(better func(a, b any) bool) aggFunc {
	return func(a *aggCall, recs []Record) any {
		var best any

		for _, r := range recs {
			if v := a.arg.eval(r); v != nil && (best == nil || better(v, best)) {
				best = v
			}
		}

		return best
	}
}

// timeAgg returns the argument from the event with the earliest (sign -1)
// or latest (sign 1) @timestamp among the events that carry it.
func timeAgg(sign int) aggFunc {
	return func(a *aggCall, recs []Record) any {
		var best, bestTS any

		for _, r := range recs {
			v, ts := a.arg.eval(r), r[FieldTimestamp]
			if v == nil || ts == nil {
				continue
			}

			if bestTS == nil || compare(ts, bestTS)*sign > 0 {
				best, bestTS = v, ts
			}
		}

		return best
	}
}

// aggPercentile is the nearest-rank percentile of the numeric values.
func aggPercentile(a *aggCall, recs []Record) any {
	v := numbers(a, recs)
	if len(v) == 0 {
		return nil
	}

	sort.Float64s(v)

	rank := math.Ceil(a.pct/maxPercent*float64(len(v))) - 1

	i, ok := clampIndex(rank, len(v)-1)
	if !ok {
		return nil
	}

	return v[i]
}

// parseAgg parses an aggregation call's arguments; the opening parenthesis
// is already consumed. Aggregations do not nest.
func (p *exprParser) parseAgg(fn *token, f aggFunc) (expr, error) {
	name := strings.ToLower(fn.text)
	call := &aggCall{fn: f}

	switch {
	case name == aggCount && p.atStarOrClose():
		if t := p.peek(); t.kind == tOp {
			p.pos++
		}
	default:
		arg, err := p.withoutAggs(p.parseOr)
		if err != nil {
			return nil, err
		}

		call.arg = arg
	}

	if name == aggPct {
		if err := p.parsePercent(fn, call); err != nil {
			return nil, err
		}
	}

	if err := p.closeParen(); err != nil {
		return nil, err
	}

	call.key = "\x00agg" + strconv.Itoa(len(*p.aggs))
	*p.aggs = append(*p.aggs, call)

	return call, nil
}

// atStarOrClose reports whether count's argument is * or empty.
func (p *exprParser) atStarOrClose() bool {
	t := p.peek()

	return t != nil && (t.kind == tRParen || (t.kind == tOp && t.text == "*"))
}

func (p *exprParser) parsePercent(fn *token, call *aggCall) error {
	if !p.comma() {
		return compileErr(fn.start, fn.end, "pct takes a field and a percentile")
	}

	t := p.peek()
	if t == nil || t.kind != tNumber {
		return p.errHere("percentile number expected in pct")
	}

	f, err := strconv.ParseFloat(t.text, 64)
	if err != nil || f < 0 || f > maxPercent {
		return compileErr(t.start, t.end, "percentile must be a number from 0 to 100")
	}

	p.pos++
	call.pct = f

	return nil
}

// withoutAggs runs a sub-parse with aggregation functions disallowed, for
// the arguments of an aggregation.
func (p *exprParser) withoutAggs(parse func() (expr, error)) (expr, error) {
	saved := p.aggs
	p.aggs = nil

	x, err := parse()

	p.aggs = saved

	return x, err
}
