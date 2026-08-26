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
		Dimensions:         alarmDimensions(c.dimensions, props),
		AlarmActions:       actionGroupIDs(props),
	}

	return h.mon.CreateAlarm(r.Context(), cfg)
}

// alarmDimensions maps a metric-alert's evaluation scope onto the driver's
// dimension filter: the criterion's own dimension filters, plus — when the alert
// targets exactly one scope — a "resourceId" dimension pinning evaluation to
// that resource, so the alert does not fire on another resource's datapoints
// sharing the same namespace/metric. A multi-scope alert leaves resourceId
// unset (aggregating across its scopes), matching the dimensionless default.
func alarmDimensions(criteriaDims map[string]string, props map[string]any) map[string]string {
	out := make(map[string]string, len(criteriaDims)+1)
	for k, v := range criteriaDims {
		out[k] = v
	}

	if scope, ok := singleScope(props); ok {
		out["resourceId"] = scope
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// singleScope returns properties.scopes[0] when the alert targets exactly one
// scope, reporting ok=false otherwise (no scopes, or multiple scopes).
func singleScope(props map[string]any) (string, bool) {
	scopes, ok := props["scopes"].([]any)
	if !ok || len(scopes) != 1 {
		return "", false
	}

	scope, ok := scopes[0].(string)
	if !ok || scope == "" {
		return "", false
	}

	return scope, true
}

// actionGroupIDs extracts properties.actions[].actionGroupId — the action
// group resource ids linked to a metric alert — and stores them on the alarm
// so DescribeAlarms echoes the linkage back (mirroring the AWS CloudWatch
// alarm's AlarmActions field). Actual delivery to the action group on a breach
// is not simulated. Entries with no actionGroupId are skipped rather than
// producing an empty AlarmActions entry.
func actionGroupIDs(props map[string]any) []string {
	actions, ok := props["actions"].([]any)
	if !ok {
		return nil
	}

	ids := make([]string, 0, len(actions))

	for _, a := range actions {
		item, ok := a.(map[string]any)
		if !ok {
			continue
		}

		if id := stringField(item, "actionGroupId"); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

// criterion is one parsed static-threshold metric criterion.
type criterion struct {
	metricName      string
	metricNamespace string
	operator        string
	threshold       float64
	timeAggregation string
	dimensions      map[string]string
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
		dimensions:      criterionDimensions(item),
	}, true
}

// criterionDimensions maps a criterion's dimension filters onto the driver's
// single-value equality model: an "Include" filter naming exactly one value
// becomes name -> value. Multi-value or "Exclude" filters can't be expressed as
// a single equality and are skipped (the alert still evaluates on the remaining
// filters), an intentional subset of Azure's dimension operators.
func criterionDimensions(item map[string]any) map[string]string {
	raw, ok := item["dimensions"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}

	out := make(map[string]string)

	for _, entry := range raw {
		if name, value, ok := singleValueDimension(entry); ok {
			out[name] = value
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// singleValueDimension reduces one criterion dimension filter to a name/value
// equality, reporting ok=false for anything the single-value model can't
// express: a non-object entry, a missing name, an "Exclude" operator, or a
// filter that doesn't name exactly one value.
func singleValueDimension(entry any) (name, value string, ok bool) {
	dim, ok := entry.(map[string]any)
	if !ok {
		return "", "", false
	}

	name = stringField(dim, "name")
	values, valuesOK := dim["values"].([]any)

	if name == "" || !valuesOK || len(values) != 1 {
		return "", "", false
	}

	if op := stringField(dim, "operator"); op != "" && op != "Include" {
		return "", "", false
	}

	value, ok = values[0].(string)
	if !ok || value == "" {
		return "", "", false
	}

	return name, value, true
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
