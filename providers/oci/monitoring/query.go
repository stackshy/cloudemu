package monitoring

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Portable statistic names and the query-language functions they map to.
const (
	statAverage = "Average"
	statSum     = "Sum"
	statMinimum = "Minimum"
	statMaximum = "Maximum"
	statCount   = "SampleCount"

	fnMean  = "mean"
	fnSum   = "sum"
	fnMin   = "min"
	fnMax   = "max"
	fnCount = "count"
)

// Dimension operators MQL's predicate accepts. The two equality operators are
// exact string tests; the two pattern operators are recognized only to refuse
// them, since guessing their pattern flavor would answer wrongly rather than
// emptily.
const (
	opEqual    = "="
	opNotEqual = "!="
	opMatch    = "=~"
	opNotMatch = "!~"
)

// dimensionPredicate is one {k <op> "v"} test from a selector's predicate.
type dimensionPredicate struct {
	key   string
	op    string
	value string
}

// selector is the metric half of an OCI query, e.g. CpuUtilization[1m].mean().
// Dimensions come from the optional {k = "v"} predicate between the two.
type selector struct {
	metricName string
	stat       string
	interval   time.Duration
	dimensions []dimensionPredicate
}

// condition is a selector plus the threshold comparison an alarm query adds.
type condition struct {
	selector
	operator  string
	threshold float64
}

// comparison pairs an alarm query's operator with its canonical name. Longer
// symbols come first so ">=" is not read as ">".
type comparison struct {
	symbol string
	name   string
}

func comparisons() []comparison {
	return []comparison{
		{">=", "GreaterThanOrEqualToThreshold"},
		{"<=", "LessThanOrEqualToThreshold"},
		{"==", "EqualToThreshold"},
		{"!=", "NotEqualToThreshold"},
		{">", "GreaterThanThreshold"},
		{"<", "LessThanThreshold"},
	}
}

// parseSelector reads the metric half of OCI's query language.
func parseSelector(query string) (selector, error) {
	sel, _, err := splitSelector(query)

	return sel, err
}

// splitSelector parses a selector and returns the unconsumed remainder for a
// caller that expects a comparison after it.
func splitSelector(query string) (sel selector, rest string, err error) {
	q := strings.TrimSpace(query)

	openIdx := strings.Index(q, "[")
	closeIdx := strings.Index(q, "]")

	if openIdx <= 0 || closeIdx <= openIdx {
		return selector{}, "", malformed(query)
	}

	dimensions, tail, err := splitDimensions(q[closeIdx+1:], query)
	if err != nil {
		return selector{}, "", err
	}

	paren := strings.Index(tail, "(")
	end := strings.Index(tail, ")")

	if !strings.HasPrefix(tail, ".") || paren < 1 || end < paren {
		return selector{}, "", malformed(query)
	}

	stat, ok := statFor(tail[1:paren])
	if !ok {
		return selector{}, "", malformed(query)
	}

	return selector{
		metricName: strings.TrimSpace(q[:openIdx]),
		stat:       stat,
		interval:   alarmDuration(strings.TrimSpace(q[openIdx+1 : closeIdx])),
		dimensions: dimensions,
	}, tail[end+1:], nil
}

// splitDimensions reads MQL's optional dimension predicate — the
// {k = "v", ...} that scopes a selector to one series — and the rest after it.
func splitDimensions(tail, query string) (dimensions []dimensionPredicate, rest string, err error) {
	if !strings.HasPrefix(tail, "{") {
		return nil, tail, nil
	}

	closeIdx := strings.Index(tail, "}")
	if closeIdx < 0 {
		return nil, "", malformed(query)
	}

	dimensions, err = parsePredicates(tail[1:closeIdx])
	if err != nil {
		return nil, "", err
	}

	return dimensions, tail[closeIdx+1:], nil
}

// parsePredicates reads the comma-separated body of a dimension predicate.
func parsePredicates(body string) ([]dimensionPredicate, error) {
	out := make([]dimensionPredicate, 0, strings.Count(body, ",")+1)

	for _, pair := range strings.Split(body, ",") {
		key, op, value, ok := splitPredicate(pair)
		if !ok || key == "" {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "malformed dimension predicate %q", pair)
		}

		if op == opMatch || op == opNotMatch {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"dimension operator %q is not supported; use %q or %q", op, opEqual, opNotEqual)
		}

		out = append(out, dimensionPredicate{key: key, op: op, value: value})
	}

	return out, nil
}

// splitPredicate cuts one k <op> "v" pair at its operator. The earliest
// operator wins, and the longest at that position, so neither is a value
// containing one mistaken for the operator nor "=~" read as "=".
func splitPredicate(pair string) (key, op, value string, ok bool) {
	at := -1

	for _, candidate := range []string{opEqual, opNotEqual, opMatch, opNotMatch} {
		i := strings.Index(pair, candidate)
		if i < 0 {
			continue
		}

		if at < 0 || i < at || (i == at && len(candidate) > len(op)) {
			at, op = i, candidate
		}
	}

	if at < 0 {
		return "", "", "", false
	}

	return strings.TrimSpace(pair[:at]), op, unquote(pair[at+len(op):]), true
}

func unquote(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"`)
}

// parseQuery reads the single-metric threshold form of OCI's query language.
// Anything richer is refused at create time rather than stored to never fire.
func parseQuery(query string) (condition, error) {
	sel, rest, err := splitSelector(query)
	if err != nil {
		return condition{}, err
	}

	operator, threshold, ok := parseComparison(rest)
	if !ok {
		return condition{}, malformed(query)
	}

	return condition{selector: sel, operator: operator, threshold: threshold}, nil
}

func malformed(query string) error {
	return cerrors.Newf(cerrors.InvalidArgument, "malformed query %q", query)
}

// matches evaluates one predicate against a series' dimensions. A series
// without the key reads as empty, which "!=" therefore accepts.
func (p *dimensionPredicate) matches(dimensions map[string]string) bool {
	if p.op == opNotEqual {
		return dimensions[p.key] != p.value
	}

	return dimensions[p.key] == p.value
}

func parseComparison(raw string) (operator string, threshold float64, ok bool) {
	s := strings.TrimSpace(raw)

	for _, c := range comparisons() {
		if !strings.HasPrefix(s, c.symbol) {
			continue
		}

		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(s, c.symbol)), 64)
		if err != nil {
			return "", 0, false
		}

		return c.name, v, true
	}

	return "", 0, false
}

// compare evaluates an alarm's threshold condition.
func compare(value, threshold float64, operator string) bool {
	switch operator {
	case "GreaterThanThreshold":
		return value > threshold
	case "GreaterThanOrEqualToThreshold":
		return value >= threshold
	case "LessThanThreshold":
		return value < threshold
	case "LessThanOrEqualToThreshold":
		return value <= threshold
	case "EqualToThreshold":
		return value == threshold
	case "NotEqualToThreshold":
		return value != threshold
	default:
		return false
	}
}

// formatSelector renders a metric selector in OCI's query language.
func formatSelector(metricName, stat string, period int, dimensions map[string]string) string {
	if period <= 0 {
		period = defaultPeriod
	}

	return fmt.Sprintf("%s[%s]%s.%s()", metricName,
		resolutionLabel(time.Duration(period)*time.Second), formatDimensions(dimensions), mqlStat(stat))
}

// formatDimensions renders a dimension set as MQL's predicate, sorted so the
// same set always produces the same query.
func formatDimensions(dimensions map[string]string) string {
	if len(dimensions) == 0 {
		return ""
	}

	keys := make([]string, 0, len(dimensions))
	for k := range dimensions {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s = %q", k, dimensions[k]))
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// formatQuery renders a portable threshold alarm as the query OCI stores. The
// alarm's dimensions become a predicate, the only place the spec can keep them.
func formatQuery(cfg *driver.AlarmConfig) (string, error) {
	operator, ok := mqlOperator(cfg.ComparisonOperator)
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "unknown comparison operator %q", cfg.ComparisonOperator)
	}

	// A non-finite threshold renders as a token no parser reads back, which
	// would leave the alarm stored but permanently unable to fire.
	if math.IsNaN(cfg.Threshold) || math.IsInf(cfg.Threshold, 0) {
		return "", cerrors.Newf(cerrors.InvalidArgument, "threshold %v is not a finite number", cfg.Threshold)
	}

	return fmt.Sprintf("%s %s %g",
		formatSelector(cfg.MetricName, cfg.Stat, cfg.Period, cfg.Dimensions), operator, cfg.Threshold), nil
}

// statFor maps a query's aggregation function onto the portable statistic,
// rejecting a function this parser does not implement.
func statFor(name string) (stat string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case fnMean:
		return statAverage, true
	case fnSum:
		return statSum, true
	case fnMin:
		return statMinimum, true
	case fnMax:
		return statMaximum, true
	case fnCount:
		return statCount, true
	default:
		return "", false
	}
}

// mqlStat is statFor's inverse. An unnamed statistic averages, as OCI does.
func mqlStat(stat string) string {
	switch stat {
	case statSum:
		return fnSum
	case statMinimum:
		return fnMin
	case statMaximum:
		return fnMax
	case statCount:
		return fnCount
	default:
		return fnMean
	}
}

func mqlOperator(operator string) (symbol string, ok bool) {
	for _, c := range comparisons() {
		if c.name == operator {
			return c.symbol, true
		}
	}

	return "", false
}

// resolutionLabel renders an aggregation interval in OCI's interval notation.
func resolutionLabel(step time.Duration) string {
	switch {
	case step%time.Hour == 0:
		return fmt.Sprintf("%dh", step/time.Hour)
	case step%time.Minute == 0:
		return fmt.Sprintf("%dm", step/time.Minute)
	default:
		return fmt.Sprintf("%ds", int(step.Seconds()))
	}
}
