package insights

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// period is a bin/datefloor/dateceil period: a fixed duration, or a whole
// number of calendar months for the month, quarter and year units.
type period struct {
	d      time.Duration
	months int
}

// Calendar units, in months.
const (
	monthsPerQuarter = 3
	monthsPerYear    = 12
)

// unitMin is the minute unit, spelled like the min aggregation.
const unitMin = "min"

// maxPeriod bounds a period so the float-to-integer conversion is safe.
const maxPeriod = 100 * 365 * 24 * time.Hour

//nolint:gochecknoglobals // fixed unit table.
var durationUnits = map[string]time.Duration{
	"ms": time.Millisecond, "msec": time.Millisecond, "millisecond": time.Millisecond,
	"s": time.Second, "sec": time.Second, "second": time.Second,
	"m": time.Minute, unitMin: time.Minute, "minute": time.Minute,
	"h": time.Hour, "hr": time.Hour, "hour": time.Hour,
	"d": 24 * time.Hour, "day": 24 * time.Hour,
	"w": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour,
}

//nolint:gochecknoglobals // fixed unit table.
var monthUnits = map[string]int{
	"mo": 1, "mon": 1, "month": 1,
	"q": monthsPerQuarter, "qtr": monthsPerQuarter, "quarter": monthsPerQuarter,
	"y": monthsPerYear, "yr": monthsPerYear, "year": monthsPerYear,
}

// parsePeriod reads a period literal such as 5m, 1h or 2qtrs. Units longer
// than one character may take a plural s.
func parsePeriod(t *token) (period, error) {
	if n, unit, ok := splitPeriod(t); ok {
		if p, ok := durationPeriod(n, unit); ok {
			return p, nil
		}

		if p, ok := monthPeriod(n, unit); ok {
			return p, nil
		}
	}

	return period{}, compileErr(t.start, t.end, "invalid time period '"+t.text+"'")
}

// splitPeriod splits a period literal into its positive, finite amount
// and its lower-case unit.
func splitPeriod(t *token) (n float64, unit string, ok bool) {
	split := strings.IndexFunc(t.text, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if t.kind != tNumber || split <= 0 {
		return 0, "", false
	}

	n, err := strconv.ParseFloat(t.text[:split], 64)
	if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, "", false
	}

	return n, strings.ToLower(t.text[split:]), true
}

func lookupUnit[V any](table map[string]V, unit string) (V, bool) {
	if v, ok := table[unit]; ok {
		return v, true
	}

	if len(unit) > twoChars && strings.HasSuffix(unit, "s") {
		v, ok := table[unit[:len(unit)-1]]
		return v, ok
	}

	var zero V

	return zero, false
}

func durationPeriod(n float64, unit string) (period, bool) {
	u, ok := lookupUnit(durationUnits, unit)
	if !ok {
		return period{}, false
	}

	ns := n * float64(u)
	if ns < float64(time.Millisecond) || ns > float64(maxPeriod) {
		return period{}, false
	}

	return period{d: time.Duration(ns)}, true
}

func monthPeriod(n float64, unit string) (period, bool) {
	u, ok := lookupUnit(monthUnits, unit)
	if !ok {
		return period{}, false
	}

	months := n * float64(u)
	if months != math.Trunc(months) || months > float64(maxPeriod/(30*24*time.Hour)) {
		return period{}, false
	}

	return period{months: int(months)}, true
}

// floor truncates t to the start of its period. Fixed periods align to the
// Unix epoch; calendar periods to the start of the month, quarter or year.
func (p period) floor(t time.Time) time.Time {
	t = t.UTC()

	if p.months > 0 {
		total := t.Year()*monthsPerYear + int(t.Month()) - 1
		total -= mod(total, p.months)

		return time.Date(total/monthsPerYear, time.Month(total%monthsPerYear+1), 1, 0, 0, 0, 0, time.UTC)
	}

	ms := t.UnixMilli()
	step := p.d.Milliseconds()

	return time.UnixMilli(ms - mod64(ms, step)).UTC()
}

func (p period) ceil(t time.Time) time.Time {
	f := p.floor(t)
	if f.Equal(t) {
		return f
	}

	if p.months > 0 {
		return f.AddDate(0, p.months, 0)
	}

	return f.Add(p.d)
}

func mod(a, b int) int { return ((a % b) + b) % b }

func mod64(a, b int64) int64 { return ((a % b) + b) % b }

// maxEpochMillis bounds a numeric timestamp so its int64 conversion is safe.
const maxEpochMillis = 1e15

// toTime reads a timestamp value: the rendered form of @timestamp, or a
// number of epoch milliseconds.
func toTime(v any) (time.Time, bool) {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(timestampLayout, s); err == nil {
			return t, true
		}
	}

	f, ok := toNum(v)
	if !ok || math.IsNaN(f) || math.Abs(f) > maxEpochMillis {
		return time.Time{}, false
	}

	return time.UnixMilli(int64(f)).UTC(), true
}

// roundExpr is bin(period), which rounds @timestamp down to the period, and
// datefloor(x, period) / dateceil(x, period).
type roundExpr struct {
	x    expr
	p    period
	ceil bool
}

func (e roundExpr) eval(rec Record) any {
	t, ok := toTime(e.x.eval(rec))
	if !ok {
		return nil
	}

	if e.ceil {
		t = e.p.ceil(t)
	} else {
		t = e.p.floor(t)
	}

	return t.Format(timestampLayout)
}

// Date functions that take a period literal.
const (
	fnBin       = "bin"
	fnDateFloor = "datefloor"
	fnDateCeil  = "dateceil"
)

func isDateFunc(name string) bool {
	return name == fnBin || name == fnDateFloor || name == fnDateCeil
}

// parseDateFunc parses the arguments of bin(period), datefloor(x, period)
// or dateceil(x, period); the opening parenthesis is already consumed.
func (p *exprParser) parseDateFunc(fn *token) (expr, error) {
	name := strings.ToLower(fn.text)

	var x expr = fieldRef{name: FieldTimestamp}

	if name == fnBin {
		p.refs = append(p.refs, token{kind: tIdent, text: FieldTimestamp, start: fn.start, end: fn.end})
	} else {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}

		if !p.comma() {
			return nil, p.errHere("expected ',' and a time period in " + fn.text)
		}

		x = arg
	}

	t := p.peek()
	if t == nil {
		return nil, p.errHere("time period expected in " + fn.text)
	}

	per, err := parsePeriod(t)
	if err != nil {
		return nil, err
	}

	p.pos++

	if err := p.closeParen(); err != nil {
		return nil, err
	}

	return roundExpr{x: x, p: per, ceil: name == fnDateCeil}, nil
}

func (p *exprParser) closeParen() error {
	if t := p.peek(); t == nil || t.kind != tRParen {
		return p.errHere("expected ')'")
	}

	p.pos++

	return nil
}
