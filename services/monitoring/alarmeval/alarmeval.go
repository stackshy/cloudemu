// Package alarmeval implements the CloudWatch-style metric-alarm evaluation
// shared by the AWS, Azure, and GCP monitoring providers: exact metric-series
// dimension matching, per-statistic aggregation of the three PutMetricData datum
// forms, and the per-Period M-of-N rule with OK recovery and TreatMissingData
// handling. Keeping this in one place stops the three providers from drifting.
package alarmeval

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Alarm states, matching the CloudWatch StateValue enum.
const (
	StateAlarm = "ALARM"
	StateOK    = "OK"
)

// defaultPeriodSeconds is the period assumed when an alarm omits one.
const defaultPeriodSeconds = 60

// TreatMissingData policies from PutMetricAlarm. Any other value (including the
// empty string) is the AWS default "missing": a period with no data is simply
// not counted toward the M-of-N rule.
const (
	treatMissingBreaching    = "breaching"
	treatMissingNotBreaching = "notBreaching"
)

// Params describes one alarm's thresholds for a single evaluation.
type Params struct {
	Period             int
	EvaluationPeriods  int
	DatapointsToAlarm  int // M in the M-of-N rule; 0 defaults to EvaluationPeriods
	Stat               string
	ComparisonOperator string
	Threshold          float64
	TreatMissingData   string
}

// normalize applies the defaults CloudWatch uses for an omitted Period,
// EvaluationPeriods, or DatapointsToAlarm.
func (p *Params) normalize() (periodDur time.Duration, evalPeriods, datapointsToAlarm int) {
	period := p.Period
	if period <= 0 {
		period = defaultPeriodSeconds
	}

	evalPeriods = p.EvaluationPeriods
	if evalPeriods <= 0 {
		evalPeriods = 1
	}

	datapointsToAlarm = p.DatapointsToAlarm
	if datapointsToAlarm <= 0 || datapointsToAlarm > evalPeriods {
		datapointsToAlarm = evalPeriods
	}

	return time.Duration(period) * time.Second, evalPeriods, datapointsToAlarm
}

// WindowStart is the earliest timestamp an evaluation of p at now considers, so
// a provider can pre-filter its stored datums to the evaluation window.
func (p *Params) WindowStart(now time.Time) time.Time {
	periodDur, evalPeriods, _ := p.normalize()

	return now.Add(-periodDur * time.Duration(evalPeriods))
}

// MatchDimensions reports whether a datum belongs to the metric series a query
// identifies. CloudWatch treats each unique combination of dimensions as a
// separate metric, so the datum's dimension set must equal the query's exactly:
// a query with fewer (or no) dimensions does not match a datum published with a
// superset, and vice versa.
func MatchDimensions(dataDims, filterDims map[string]string) bool {
	if len(dataDims) != len(filterDims) {
		return false
	}

	for k, v := range filterDims {
		if dataDims[k] != v {
			return false
		}
	}

	return true
}

// MatchAlarmDimensions reports whether a datum contributes to a metric alert's
// evaluation. Unlike MatchDimensions — which pins down one exact metric series
// for a read query, matching how CloudWatch alarms monitor a single series —
// Azure Monitor and GCP alerting aggregate across unspecified dimensions: a
// criterion carrying no dimension filter evaluates over ALL timeseries of the
// metric, and a filter naming some dimensions matches any datum whose dimensions
// CONTAIN them (a superset is allowed). AWS/CloudWatch alarm evaluation keeps
// using MatchDimensions; the aggregating providers use this.
func MatchAlarmDimensions(dataDims, filterDims map[string]string) bool {
	for k, v := range filterDims {
		if dataDims[k] != v {
			return false
		}
	}

	return true
}

// EvaluateComparison reports whether value crosses threshold under operator.
func EvaluateComparison(value float64, operator string, threshold float64) bool {
	switch operator {
	case "GreaterThanThreshold":
		return value > threshold
	case "GreaterThanOrEqualToThreshold":
		return value >= threshold
	case "LessThanThreshold":
		return value < threshold
	case "LessThanOrEqualToThreshold":
		return value <= threshold
	default:
		return false
	}
}

// StatOf aggregates every datum in the slice and returns the requested statistic,
// or 0 when the slice is empty. It folds a plain Value, a StatisticValues set,
// and paired Values/Counts arrays uniformly.
func StatOf(datums []driver.MetricDatum, stat string) float64 {
	return aggregate(datums).stat(stat)
}

// EvaluateWindow applies CloudWatch's M-of-N rule. It groups datums (already
// filtered to the alarm's metric series and evaluation window) into the last
// EvaluationPeriods per-Period buckets — bucket 0 is the most recent period —
// evaluates the statistic per bucket, and returns ALARM when at least
// DatapointsToAlarm buckets breach, otherwise OK (which is how an alarm recovers
// once the breaching periods age out of the window). Empty periods are counted
// per TreatMissingData. evaluated is false when there is nothing to evaluate (all
// periods missing under a non-breaching policy), so the caller leaves the state
// unchanged rather than forcing a transition.
func EvaluateWindow(datums []driver.MetricDatum, p *Params, now time.Time) (state, reason string, evaluated bool) {
	periodDur, evalPeriods, datapointsToAlarm := p.normalize()
	buckets := bucketByPeriod(datums, now, periodDur, evalPeriods)

	breaching, present := 0, 0

	for _, b := range buckets {
		switch {
		case b != nil:
			present++

			if EvaluateComparison(b.stat(p.Stat), p.ComparisonOperator, p.Threshold) {
				breaching++
			}
		case p.TreatMissingData == treatMissingBreaching:
			present++
			breaching++
		case p.TreatMissingData == treatMissingNotBreaching:
			present++
		}
	}

	if present == 0 {
		return "", "", false
	}

	if breaching >= datapointsToAlarm {
		return StateAlarm, "Threshold crossed", true
	}

	return StateOK, "Threshold not crossed", true
}

// bucketByPeriod groups datums into evalPeriods accumulators indexed by age,
// where bucket 0 covers the most recent period. A nil bucket had no data.
func bucketByPeriod(datums []driver.MetricDatum, now time.Time, periodDur time.Duration, evalPeriods int) []*statAgg {
	buckets := make([]*statAgg, evalPeriods)

	for i := range datums {
		age := now.Sub(datums[i].Timestamp)
		if age < 0 {
			continue
		}

		idx := int(age / periodDur)
		if idx >= evalPeriods {
			continue
		}

		if buckets[idx] == nil {
			buckets[idx] = &statAgg{}
		}

		foldDatum(buckets[idx], &datums[i])
	}

	return buckets
}

// statAgg accumulates SampleCount / Sum / Minimum / Maximum across a set of
// metric datums so any requested statistic can be derived. It treats a plain
// Value, a pre-aggregated StatisticValues set, and paired Values/Counts arrays
// uniformly, matching how real CloudWatch folds all three into one series.
type statAgg struct {
	count float64
	sum   float64
	min   float64
	max   float64
	seen  bool
}

// add folds one observation (or sub-aggregate) into the accumulator: count
// samples summing to sum, whose smallest and largest observed values are low
// and high. Non-positive counts contribute nothing, matching AWS.
func (a *statAgg) add(count, sum, low, high float64) {
	if count <= 0 {
		return
	}

	a.count += count
	a.sum += sum

	if !a.seen || low < a.min {
		a.min = low
	}

	if !a.seen || high > a.max {
		a.max = high
	}

	a.seen = true
}

// stat returns the requested statistic, or 0 when no data was accumulated.
//
// The accumulator keeps only count/sum/min/max, so a true percentile (an
// ExtendedStatistic such as p95) is not computable from it — a percentile needs
// the raw sample distribution. An alarm configured with only an ExtendedStatistic
// passes an empty Stat here and is therefore approximated by Average. This is a
// documented approximation (tracked with the deferred percentile support), not a
// silently wrong answer; it keeps such an alarm evaluating rather than erroring.
func (a statAgg) stat(stat string) float64 {
	if !a.seen {
		return 0
	}

	switch stat {
	case "Sum":
		return a.sum
	case "Min", "Minimum":
		return a.min
	case "Max", "Maximum":
		return a.max
	case "SampleCount":
		return a.count
	default: // "Average", unspecified, or an ExtendedStatistic percentile (approximated)
		return a.sum / a.count
	}
}

// aggregate folds every datum into a single accumulator.
func aggregate(datums []driver.MetricDatum) statAgg {
	var a statAgg

	for i := range datums {
		foldDatum(&a, &datums[i])
	}

	return a
}

// foldDatum folds one datum — plain Value, StatisticValues set, or Values/Counts
// arrays — into the accumulator.
func foldDatum(a *statAgg, d *driver.MetricDatum) {
	switch {
	case d.StatisticValues != nil:
		s := d.StatisticValues
		a.add(s.SampleCount, s.Sum, s.Minimum, s.Maximum)
	case len(d.Values) > 0:
		for j, v := range d.Values {
			count := 1.0
			if j < len(d.Counts) {
				count = d.Counts[j]
			}

			a.add(count, v*count, v, v)
		}
	default:
		a.add(1, d.Value, d.Value, d.Value)
	}
}
