package monitor

import (
	"net/http"
	"strconv"
	"strings"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	defaultWindowSeconds = 300
	opGreaterThan        = "GreaterThanThreshold"
)

// registerAlarm bridges an Azure metric-alert definition onto the monitoring
// driver so the named metric is actually evaluated. It reads the first static
// threshold criterion from properties.criteria.allOf and maps the ARM operator /
// timeAggregation / windowSize onto the driver's AlarmConfig. A definition with
// no usable criterion is stored (echoed on read) but not evaluated.
func (h *Handler) registerAlarm(r *http.Request, name string, props map[string]any) error {
	c, ok := firstCriterion(props)
	if !ok {
		return nil
	}

	cfg := mondriver.AlarmConfig{
		Name:               name,
		Namespace:          c.metricNamespace,
		MetricName:         c.metricName,
		ComparisonOperator: mapOperator(c.operator),
		Threshold:          c.threshold,
		Period:             windowSeconds(props),
		EvaluationPeriods:  1,
		Stat:               mapAggregation(c.timeAggregation),
	}

	return h.mon.CreateAlarm(r.Context(), cfg)
}

// criterion is one parsed static-threshold metric criterion.
type criterion struct {
	metricName      string
	metricNamespace string
	operator        string
	threshold       float64
	timeAggregation string
}

// firstCriterion extracts the first entry of properties.criteria.allOf.
func firstCriterion(props map[string]any) (criterion, bool) {
	criteria, ok := props["criteria"].(map[string]any)
	if !ok {
		return criterion{}, false
	}

	allOf, ok := criteria["allOf"].([]any)
	if !ok || len(allOf) == 0 {
		return criterion{}, false
	}

	item, ok := allOf[0].(map[string]any)
	if !ok {
		return criterion{}, false
	}

	name, ok := item["metricName"].(string)
	if !ok || name == "" {
		return criterion{}, false
	}

	return criterion{
		metricName:      name,
		metricNamespace: stringField(item, "metricNamespace"),
		operator:        stringField(item, "operator"),
		threshold:       floatField(item["threshold"]),
		timeAggregation: stringField(item, "timeAggregation"),
	}, true
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)

	return s
}

// floatField coerces a JSON number (float64) or numeric string to float64.
func floatField(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)

		return f
	default:
		return 0
	}
}

// mapOperator maps an Azure metric-alert operator to a driver comparison
// operator. An unrecognized operator falls back to GreaterThanThreshold.
func mapOperator(op string) string {
	switch op {
	case "GreaterThanOrEqual":
		return "GreaterThanOrEqualToThreshold"
	case "LessThan":
		return "LessThanThreshold"
	case "LessThanOrEqual":
		return "LessThanOrEqualToThreshold"
	default: // "GreaterThan" and any unrecognized operator
		return opGreaterThan
	}
}

// mapAggregation maps an Azure timeAggregation to a driver statistic.
func mapAggregation(agg string) string {
	switch agg {
	case "Total":
		return "Sum"
	case "Count":
		return "SampleCount"
	case "Minimum", "Maximum":
		return agg
	default:
		return "Average"
	}
}

// windowSeconds reads properties.windowSize (an ISO-8601 duration) as seconds,
// defaulting to five minutes when absent or unparseable.
func windowSeconds(props map[string]any) int {
	iso, ok := props["windowSize"].(string)
	if !ok {
		return defaultWindowSeconds
	}

	if secs := parseISODuration(iso); secs > 0 {
		return secs
	}

	return defaultWindowSeconds
}

// parseISODuration parses the minute/hour/day subset of ISO-8601 durations that
// metric-alert windows use (PT1M, PT5M, PT1H, PT6H, P1D). Returns 0 when the
// string is not one of those shapes.
func parseISODuration(s string) int {
	const (
		secsPerMinute = 60
		secsPerHour   = 3600
		secsPerDay    = 86400
	)

	if num, ok := durationNumber(s, "PT", "M"); ok {
		return num * secsPerMinute
	}

	if num, ok := durationNumber(s, "PT", "H"); ok {
		return num * secsPerHour
	}

	if num, ok := durationNumber(s, "P", "D"); ok {
		return num * secsPerDay
	}

	return 0
}

// durationNumber pulls the integer between prefix and suffix (e.g. "5" from
// "PT5M"), reporting ok=false when the string does not have that shape.
func durationNumber(s, prefix, suffix string) (int, bool) {
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return 0, false
	}

	mid := strings.TrimSuffix(strings.TrimPrefix(s, prefix), suffix)

	n, err := strconv.Atoi(mid)
	if err != nil {
		return 0, false
	}

	return n, true
}
