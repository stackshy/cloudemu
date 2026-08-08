package monitoring

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

// selector is the metric half of an OCI query, e.g. CpuUtilization[1m].mean().
type selector struct {
	metricName string
	stat       string
	interval   time.Duration
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

// parseSelector reads the metric half of OCI's query language, returning the
// unconsumed remainder for a caller that expects a comparison after it.
func parseSelector(query string) (selector, bool) {
	sel, _, ok := splitSelector(query)

	return sel, ok
}

func splitSelector(query string) (sel selector, rest string, ok bool) {
	q := strings.TrimSpace(query)

	openIdx := strings.Index(q, "[")
	closeIdx := strings.Index(q, "]")

	if openIdx <= 0 || closeIdx <= openIdx {
		return selector{}, "", false
	}

	tail := q[closeIdx+1:]
	paren := strings.Index(tail, "(")
	end := strings.Index(tail, ")")

	if !strings.HasPrefix(tail, ".") || paren < 1 || end < paren {
		return selector{}, "", false
	}

	return selector{
		metricName: strings.TrimSpace(q[:openIdx]),
		stat:       statFor(tail[1:paren]),
		interval:   alarmDuration(strings.TrimSpace(q[openIdx+1 : closeIdx])),
	}, tail[end+1:], true
}

// parseQuery reads the single-metric threshold form of OCI's query language.
// Anything richer is stored verbatim and simply never fires.
func parseQuery(query string) (condition, bool) {
	sel, rest, ok := splitSelector(query)
	if !ok {
		return condition{}, false
	}

	operator, threshold, ok := parseComparison(rest)
	if !ok {
		return condition{}, false
	}

	return condition{selector: sel, operator: operator, threshold: threshold}, true
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
func formatSelector(metricName, stat string, period int) string {
	if period <= 0 {
		period = defaultPeriod
	}

	return fmt.Sprintf("%s[%s].%s()", metricName, resolutionLabel(period), mqlStat(stat))
}

// formatQuery renders a portable threshold alarm as the query OCI stores.
func formatQuery(cfg *driver.AlarmConfig) string {
	return fmt.Sprintf("%s %s %g",
		formatSelector(cfg.MetricName, cfg.Stat, cfg.Period),
		mqlOperator(cfg.ComparisonOperator), cfg.Threshold)
}

// statFor maps a query's aggregation function onto the portable statistic.
func statFor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case fnSum:
		return statSum
	case fnMin:
		return statMinimum
	case fnMax:
		return statMaximum
	case fnCount:
		return statCount
	default:
		return statAverage
	}
}

// mqlStat is statFor's inverse.
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

func mqlOperator(operator string) string {
	for _, c := range comparisons() {
		if c.name == operator {
			return c.symbol
		}
	}

	return ">"
}
