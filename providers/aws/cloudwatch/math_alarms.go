package cloudwatch

import (
	"sort"
	"strings"
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

	// PutMetricAlarm requires a Period on every MetricStat, so a stored list
	// always has one.
	if q := watchedQuery(a); q != nil {
		return metricmath.Period(a.Metrics, q)
	}

	return 0
}

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
//
// The shape follows the sample payloads in aws-lambda-go
// events/testdata/cloudwatch-alarm-sns-payload-{single-metric,multiple-metrics}.json.
func notificationTrigger(a *alarmData) map[string]any {
	treat := a.TreatMissingData
	if treat == "" {
		treat = treatMissingDefault
	}

	trigger := map[string]any{
		"Period":                           alarmPeriod(a),
		"EvaluationPeriods":                a.EvaluationPeriods,
		"ComparisonOperator":               a.ComparisonOperator,
		"TreatMissingData":                 treatMissingLabel + treat,
		"EvaluateLowSampleCountPercentile": "",
	}

	// An anomaly alarm names its band instead of a threshold.
	if a.ThresholdMetricID != "" {
		trigger["ThresholdMetricId"] = a.ThresholdMetricID
	} else {
		trigger["Threshold"] = a.Threshold
	}

	if len(a.Metrics) > 0 {
		trigger["Metrics"] = notificationMetrics(a.Metrics)

		return trigger
	}

	trigger["MetricName"] = a.MetricName
	trigger["Namespace"] = a.Namespace
	trigger["Dimensions"] = notificationDimensions(a.Dimensions)
	trigger["Unit"] = nil

	if a.Unit != "" {
		trigger["Unit"] = a.Unit
	}

	if a.ExtendedStatistic != "" {
		trigger["StatisticType"] = "ExtendedStatistic"
		trigger["ExtendedStatistic"] = a.ExtendedStatistic
	} else {
		trigger["StatisticType"] = "Statistic"
		trigger["Statistic"] = strings.ToUpper(a.Stat)
	}

	return trigger
}

// treatMissingLabel prefixes TreatMissingData in a notification. CloudWatch
// sends the value as a padded label, for example
// "- TreatMissingData:                    missing".
const treatMissingLabel = "- TreatMissingData:                    "

// notificationDimensions renders dimensions sorted by name, with lower-case
// name and value keys as CloudWatch sends them.
func notificationDimensions(dims map[string]string) []map[string]string {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]string{"value": dims[k], "name": k})
	}

	return out
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
			entry["MetricStat"] = map[string]any{
				"Metric": map[string]any{"Dimensions": notificationDimensions(ms.Dimensions), "MetricName": ms.MetricName, "Namespace": ms.Namespace},
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
// bucket. An expression outside the supported syntax gives no data. For an
// anomaly alarm it also sets p.Band and creates the alarm's detector when it
// is missing. The caller holds alarmMu.
func (m *Mock) mathDatums(a *alarmData, p *alarmeval.Params, at time.Time) []driver.MetricDatum {
	q := watchedQuery(a)
	if q == nil {
		return nil
	}

	// Reads end before EndTime. Shifting the window by 1ns keeps a datum put
	// at the evaluation instant, as a plain alarm does.
	start, end := p.WindowStart(at).Add(time.Nanosecond), at.Add(time.Nanosecond)
	ev := metricmath.New(a.Metrics, m.rangeFetcher(start, end))

	bandID, inputID, isBand := bandThreshold(a)
	if isBand {
		if d, ok := detectorFor(a.Metrics, inputID); ok {
			m.ensureDetectorLocked(&d)
		}

		ev.WithBand(metricmath.BandConfig{
			History:  m.rangeFetcher(start.Add(-metricmath.TrainingWindow), end),
			Excluded: m.bandExclusionsLocked,
		})
	}

	series, err := ev.ResolveAt(q.ID, p.Period)
	if err != nil {
		return nil
	}

	half := time.Duration(p.Period) * time.Second / 2

	if isBand {
		band, _, err := ev.BandAt(bandID, p.Period)
		if err != nil {
			return nil
		}

		p.Band = bandPoints(&band, half)
	}

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

// bandPoints turns a band into evaluator points, shifted to mid-period like
// the datums.
func bandPoints(b *metricmath.Band, half time.Duration) []alarmeval.BandPoint {
	out := make([]alarmeval.BandPoint, 0, len(b.Timestamps))

	for i, ts := range b.Timestamps {
		out = append(out, alarmeval.BandPoint{Timestamp: ts.Add(half), Lower: b.Lower[i], Upper: b.Upper[i]})
	}

	return out
}

// rangeFetcher reads metrics over [start, end).
func (m *Mock) rangeFetcher(start, end time.Time) metricmath.Fetcher {
	return func(ms *driver.MetricStat, period int) (metricmath.Series, error) {
		res := m.readMetric(&driver.GetMetricInput{
			Namespace: ms.Namespace, MetricName: ms.MetricName, Dimensions: ms.Dimensions,
			StartTime: start, EndTime: end, Period: period, Stat: ms.Stat, Unit: ms.Unit,
		})

		return metricmath.Series{Timestamps: res.Timestamps, Values: res.Values}, nil
	}
}
