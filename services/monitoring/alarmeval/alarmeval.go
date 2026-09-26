// Package alarmeval implements the CloudWatch-style metric-alarm evaluation
// shared by the AWS, Azure, and GCP monitoring providers: exact metric-series
// dimension matching, per-statistic aggregation of the three PutMetricData datum
// forms, and the per-Period M-of-N rule with OK recovery and TreatMissingData
// handling. Keeping this in one place stops the three providers from drifting.
//
// Evaluation is lazy and clock based. A provider evaluates an alarm when data
// arrives, when the alarm is created or updated, and on any read that shows
// state once EvaluationInterval has passed since the last evaluation. There is
// no background ticker.
//
// The window slides. It ends at the evaluation instant and is not aligned to
// the wall clock, which is the documented CloudWatch default: "the boundaries
// of the window are not aligned to the wall clock"
// (https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarm-evaluation.html).
// One difference is kept on purpose. AWS evaluates on its own minute ticks,
// while cloudemu evaluates at the moment it looks.
package alarmeval

import (
	"fmt"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Alarm states, matching the CloudWatch StateValue enum.
const (
	StateAlarm            = "ALARM"
	StateOK               = "OK"
	StateInsufficientData = "INSUFFICIENT_DATA"
)

// ValidState reports whether s is one of the three alarm states.
func ValidState(s string) bool {
	switch s {
	case StateAlarm, StateOK, StateInsufficientData:
		return true
	default:
		return false
	}
}

// UnitNone is the unit CloudWatch gives a datum published without one.
const UnitNone = "None"

// validUnits is the CloudWatch StandardUnit enum.
//
//nolint:gochecknoglobals // closed enum
var validUnits = map[string]bool{
	"Seconds": true, "Microseconds": true, "Milliseconds": true,
	"Bytes": true, "Kilobytes": true, "Megabytes": true, "Gigabytes": true, "Terabytes": true,
	"Bits": true, "Kilobits": true, "Megabits": true, "Gigabits": true, "Terabits": true,
	"Percent": true, "Count": true,
	"Bytes/Second": true, "Kilobytes/Second": true, "Megabytes/Second": true,
	"Gigabytes/Second": true, "Terabytes/Second": true,
	"Bits/Second": true, "Kilobits/Second": true, "Megabits/Second": true,
	"Gigabits/Second": true, "Terabits/Second": true,
	"Count/Second": true, UnitNone: true,
}

// ValidUnit reports whether u is a CloudWatch StandardUnit value.
func ValidUnit(u string) bool {
	return validUnits[u]
}

// Units returns every StandardUnit value, sorted. Error messages list them.
func Units() []string {
	out := make([]string, 0, len(validUnits))
	for u := range validUnits {
		out = append(out, u)
	}

	sort.Strings(out)

	return out
}

// EffectiveUnit returns the unit a datum is stored under. An empty unit is None.
func EffectiveUnit(u string) string {
	if u == "" {
		return UnitNone
	}

	return u
}

// MatchUnit reports whether a datum stored with datumUnit is picked by a read
// or an alarm that asks for want. An empty want picks every unit. CloudWatch
// does no unit conversion, so any other want must match exactly.
func MatchUnit(datumUnit, want string) bool {
	return want == "" || EffectiveUnit(datumUnit) == want
}

// defaultPeriodSeconds is the period assumed when an alarm omits one.
const defaultPeriodSeconds = 60

// Evaluation cadences from the CloudWatch alarm-evaluation guide. A period
// under a minute is evaluated every 10 seconds. A window longer than a day is
// evaluated once an hour. Anything else is evaluated every minute.
const (
	highResInterval  = 10 * time.Second
	standardInterval = time.Minute
	multiDayInterval = time.Hour
	oneDaySeconds    = 86400
)

// TreatMissingData policies from PutMetricAlarm. Any other value, including
// the empty string, is the AWS default "missing". With "missing" an empty
// period is not counted toward the M-of-N rule.
const (
	TreatMissingIgnore       = "ignore"
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
	// IgnoreMissingByDefault makes an empty TreatMissingData act as "ignore".
	// AWS sets it for AWS/DynamoDB alarms. An explicit policy still wins.
	IgnoreMissingByDefault bool
	// Band is the anomaly band of a band-operator alarm, bucketed like the
	// datums. A period with data but no band point counts as missing.
	Band []BandPoint
}

// BandPoint is one point of an anomaly band.
type BandPoint struct {
	Timestamp time.Time
	Lower     float64
	Upper     float64
}

// Anomaly band operators. They compare against a band, not a threshold.
const (
	opOutsideBand = "LessThanLowerOrGreaterThanUpperThreshold"
	opBelowBand   = "LessThanLowerThreshold"
	opAboveBand   = "GreaterThanUpperThreshold"
)

// IsBandOperator reports whether op compares against an anomaly band.
func IsBandOperator(op string) bool {
	return op == opOutsideBand || op == opBelowBand || op == opAboveBand
}

// breachesBand reports whether value is outside the band under op.
func breachesBand(value float64, op string, b BandPoint) bool {
	switch op {
	case opOutsideBand:
		return value < b.Lower || value > b.Upper
	case opBelowBand:
		return value < b.Lower
	case opAboveBand:
		return value > b.Upper
	default:
		return false
	}
}

// Outcome is the result of one evaluation. When Retain is true the alarm
// keeps its current state and State is empty.
type Outcome struct {
	State  string
	Reason string
	Retain bool
}

// EvaluationInterval is how often CloudWatch evaluates an alarm with this
// period and number of evaluation periods.
func EvaluationInterval(period, evalPeriods int) time.Duration {
	if period <= 0 {
		period = defaultPeriodSeconds
	}

	if evalPeriods <= 0 {
		evalPeriods = 1
	}

	switch {
	case period < defaultPeriodSeconds:
		return highResInterval
	case period*evalPeriods > oneDaySeconds:
		return multiDayInterval
	default:
		return standardInterval
	}
}

// Due reports whether an alarm last evaluated at lastEval should be evaluated
// again at now. An alarm that was never evaluated is always due.
func Due(lastEval, now time.Time, interval time.Duration) bool {
	return lastEval.IsZero() || !now.Before(lastEval.Add(interval))
}

// EvaluationTime is the instant an evaluation at now looks back from. A
// multi-day alarm only sees data up to the top of the current hour, as the
// CloudWatch guide describes. Every other alarm looks back from now.
func (p *Params) EvaluationTime(now time.Time) time.Time {
	if EvaluationInterval(p.Period, p.EvaluationPeriods) == multiDayInterval {
		return now.Truncate(time.Hour)
	}

	return now
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
// evaluation. MatchDimensions pins down one exact metric series for a read query,
// matching how CloudWatch alarms monitor a single series. Azure Monitor and GCP
// alerting instead aggregate across unspecified dimensions: a
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
// EvaluationPeriods per-Period buckets, where bucket 0 is the most recent
// period. It returns ALARM when at least DatapointsToAlarm buckets breach and
// OK otherwise, which is how an alarm recovers once breaching periods age out.
// Empty periods count per TreatMissingData. When every period is empty and the
// policy does not fill them in, "missing" gives INSUFFICIENT_DATA and "ignore"
// keeps the current state.
func EvaluateWindow(datums []driver.MetricDatum, p *Params, now time.Time) Outcome {
	periodDur, evalPeriods, datapointsToAlarm := p.normalize()
	buckets := bucketByPeriod(datums, now, periodDur, evalPeriods)
	band := p.bandBuckets(now, periodDur, evalPeriods)

	breaching, present := 0, 0

	for i, b := range buckets {
		has, breach := p.judge(b, band[i])

		switch {
		case has:
			present++

			if breach {
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
		return missingOutcome(p, evalPeriods)
	}

	if breaching >= datapointsToAlarm {
		return Outcome{State: StateAlarm, Reason: "Threshold crossed"}
	}

	return Outcome{State: StateOK, Reason: "Threshold not crossed"}
}

// judge reports whether a period has a usable datapoint and whether it
// breaches. A band alarm needs a band point too.
func (p *Params) judge(b *statAgg, band *BandPoint) (has, breach bool) {
	if b == nil {
		return false, false
	}

	if !IsBandOperator(p.ComparisonOperator) {
		return true, EvaluateComparison(b.stat(p.Stat), p.ComparisonOperator, p.Threshold)
	}

	if band == nil {
		return false, false
	}

	return true, breachesBand(b.stat(p.Stat), p.ComparisonOperator, *band)
}

// bandBuckets places each band point in its period, like bucketByPeriod. The
// latest point wins when a period has more than one. A nil entry has none.
func (p *Params) bandBuckets(now time.Time, periodDur time.Duration, evalPeriods int) []*BandPoint {
	out := make([]*BandPoint, evalPeriods)

	for i := range p.Band {
		bp := &p.Band[i]

		idx, ok := bucketIndex(bp.Timestamp, now, periodDur, evalPeriods)
		if !ok {
			continue
		}

		if out[idx] == nil || bp.Timestamp.After(out[idx].Timestamp) {
			out[idx] = bp
		}
	}

	return out
}

// RecentBand returns the band edges of each period that has both a
// datapoint and a band point, oldest first. CloudWatch reports them in the
// stateReasonData of an anomaly alarm.
func RecentBand(datums []driver.MetricDatum, p *Params, now time.Time) (lower, upper []float64) {
	periodDur, evalPeriods, _ := p.normalize()
	buckets := bucketByPeriod(datums, now, periodDur, evalPeriods)
	band := p.bandBuckets(now, periodDur, evalPeriods)

	lower, upper = []float64{}, []float64{}

	for i := len(buckets) - 1; i >= 0; i-- {
		if buckets[i] != nil && band[i] != nil {
			lower = append(lower, band[i].Lower)
			upper = append(upper, band[i].Upper)
		}
	}

	return lower, upper
}

// missingOutcome is the result when every period in the window is empty and
// the policy does not fill them in.
func missingOutcome(p *Params, evalPeriods int) Outcome {
	treat := p.TreatMissingData
	if treat == "" && p.IgnoreMissingByDefault {
		treat = TreatMissingIgnore
	}

	if treat == TreatMissingIgnore {
		return Outcome{Retain: true}
	}

	noun := "datapoints were"
	if evalPeriods == 1 {
		noun = "datapoint was"
	}

	return Outcome{
		State:  StateInsufficientData,
		Reason: fmt.Sprintf("Insufficient Data: %d %s unknown.", evalPeriods, noun),
	}
}

// RecentDatapoints returns the statistic of each non-empty period in the
// evaluation window, oldest first. CloudWatch reports these in the
// stateReasonData of a metric-driven transition.
func RecentDatapoints(datums []driver.MetricDatum, p *Params, now time.Time) []float64 {
	periodDur, evalPeriods, _ := p.normalize()
	buckets := bucketByPeriod(datums, now, periodDur, evalPeriods)

	out := make([]float64, 0, len(buckets))

	for i := len(buckets) - 1; i >= 0; i-- {
		if buckets[i] != nil {
			out = append(out, buckets[i].stat(p.Stat))
		}
	}

	return out
}

// bucketByPeriod groups datums into evalPeriods accumulators indexed by age,
// where bucket 0 covers the most recent period. A nil bucket had no data.
func bucketByPeriod(datums []driver.MetricDatum, now time.Time, periodDur time.Duration, evalPeriods int) []*statAgg {
	buckets := make([]*statAgg, evalPeriods)

	for i := range datums {
		idx, ok := bucketIndex(datums[i].Timestamp, now, periodDur, evalPeriods)
		if !ok {
			continue
		}

		if buckets[idx] == nil {
			buckets[idx] = &statAgg{}
		}

		foldDatum(buckets[idx], &datums[i])
	}

	return buckets
}

// bucketIndex is the age bucket of ts, where 0 is the most recent period.
// ok is false for a future time or one older than the window.
func bucketIndex(ts, now time.Time, periodDur time.Duration, evalPeriods int) (int, bool) {
	age := now.Sub(ts)
	if age < 0 {
		return 0, false
	}

	idx := int(age / periodDur)
	if idx >= evalPeriods {
		return 0, false
	}

	return idx, true
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
// ExtendedStatistic such as p95) is not computable from it: a percentile needs
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

// foldDatum folds one datum (plain Value, StatisticValues set, or Values/Counts
// arrays) into the accumulator.
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
