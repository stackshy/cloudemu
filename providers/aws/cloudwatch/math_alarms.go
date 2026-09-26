package cloudwatch

import (
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// A metric-math alarm watches the one Metrics entry that returns data. Its
// series is computed with metricmath and each point becomes one datum, so
// alarmeval applies the same M-of-N rule as for a plain metric.

// watchedQuery returns the entry a math alarm watches, or nil.
func watchedQuery(a *alarmData) *driver.MetricDataQuery {
	watched := metricmath.Watched(a.Metrics, a.ThresholdMetricID)
	if len(watched) != 1 {
		return nil
	}

	return watched[0]
}

// alarmPeriod is the period an alarm is evaluated at. A math alarm has no
// Period of its own and uses the period of the entry it watches.
func alarmPeriod(a *alarmData) int {
	if len(a.Metrics) == 0 {
		return a.Period
	}

	if q := watchedQuery(a); q != nil {
		if p := metricmath.Period(a.Metrics, q); p > 0 {
			return p
		}
	}

	return defaultMathPeriod
}

// defaultMathPeriod is used when no entry of a math alarm sets a period.
const defaultMathPeriod = 60

// alarmReads reports whether an alarm reads any of the given metrics.
func alarmReads(a *alarmData, keys map[metricKey]bool) bool {
	if len(a.Metrics) == 0 {
		return keys[metricKey{Namespace: a.Namespace, MetricName: a.MetricName}]
	}

	for i := range a.Metrics {
		if ms := a.Metrics[i].MetricStat; ms != nil && keys[metricKey{Namespace: ms.Namespace, MetricName: ms.MetricName}] {
			return true
		}
	}

	return false
}

// notificationTrigger is the Trigger block of an SNS alarm notification. A
// math alarm has no single metric, so it lists its Metrics instead.
func notificationTrigger(a *alarmData) map[string]any {
	trigger := map[string]any{
		"ComparisonOperator": a.ComparisonOperator,
		"Threshold":          a.Threshold,
		"EvaluationPeriods":  a.EvaluationPeriods,
	}

	if len(a.Metrics) == 0 {
		trigger["MetricName"] = a.MetricName
		trigger["Namespace"] = a.Namespace
		trigger["Statistic"] = a.Stat
		trigger["Period"] = a.Period

		return trigger
	}

	treat := a.TreatMissingData
	if treat == "" {
		treat = treatMissingDefault
	}

	trigger["Period"] = alarmPeriod(a)
	trigger["TreatMissingData"] = treat
	trigger["EvaluateLowSampleCountPercentile"] = ""
	trigger["Metrics"] = notificationMetrics(a.Metrics)

	return trigger
}

// notificationMetrics renders a Metrics list in the notification shape.
// Dimensions use lower-case name and value keys, as CloudWatch sends them.
func notificationMetrics(queries []driver.MetricDataQuery) []map[string]any {
	out := make([]map[string]any, 0, len(queries))

	for i := range queries {
		q := &queries[i]
		entry := map[string]any{"Id": q.ID, "ReturnData": metricmath.ReturnsData(q)}

		if q.Expression != "" {
			entry["Expression"] = q.Expression
		}

		if q.Label != "" {
			entry["Label"] = q.Label
		}

		if ms := q.MetricStat; ms != nil {
			keys := make([]string, 0, len(ms.Dimensions))
			for k := range ms.Dimensions {
				keys = append(keys, k)
			}

			sort.Strings(keys)

			dims := make([]map[string]string, 0, len(keys))
			for _, k := range keys {
				dims = append(dims, map[string]string{"value": ms.Dimensions[k], "name": k})
			}

			entry["MetricStat"] = map[string]any{
				"Metric": map[string]any{"Dimensions": dims, "MetricName": ms.MetricName, "Namespace": ms.Namespace},
				"Period": ms.Period,
				"Stat":   ms.Stat,
			}
		}

		out = append(out, entry)
	}

	return out
}

// alarmDatums returns the data an evaluation at `at` looks at. The caller
// holds alarmMu.
func (m *Mock) alarmDatums(a *alarmData, p *alarmeval.Params, at time.Time) []driver.MetricDatum {
	if len(a.Metrics) == 0 {
		// Data stored under another unit is not seen, so an alarm with the
		// wrong unit stays in INSUFFICIENT_DATA like on AWS.
		return m.collectFilteredDatums(a.Namespace, a.MetricName, a.Dimensions, a.Unit, p.WindowStart(at), at)
	}

	return m.mathDatums(a, p, at)
}

// mathDatums computes the watched series over the window. Each point marks
// the start of its period, so its datum sits mid-period to land in the right
// bucket. An expression outside the supported syntax gives no data.
func (m *Mock) mathDatums(a *alarmData, p *alarmeval.Params, at time.Time) []driver.MetricDatum {
	q := watchedQuery(a)
	if q == nil {
		return nil
	}

	// Reads end before EndTime. Shifting the window by 1ns keeps a datum put
	// at the evaluation instant, as a plain alarm does.
	start, end := p.WindowStart(at).Add(time.Nanosecond), at.Add(time.Nanosecond)
	fetch := func(ms *driver.MetricStat, period int) (metricmath.Series, error) {
		res := m.readMetric(&driver.GetMetricInput{
			Namespace: ms.Namespace, MetricName: ms.MetricName, Dimensions: ms.Dimensions,
			StartTime: start, EndTime: end, Period: period, Stat: ms.Stat, Unit: ms.Unit,
		})

		return metricmath.Series{Timestamps: res.Timestamps, Values: res.Values}, nil
	}

	series, err := metricmath.New(a.Metrics, fetch).ResolveAt(q.ID, p.Period)
	if err != nil {
		return nil
	}

	half := time.Duration(p.Period) * time.Second / 2
	datums := make([]driver.MetricDatum, 0, len(series.Values))

	for i, ts := range series.Timestamps {
		// A constant expression has no timestamp and no place in the window.
		if ts.IsZero() {
			continue
		}

		datums = append(datums, driver.MetricDatum{Value: series.Values[i], Timestamp: ts.Add(half)})
	}

	return datums
}
