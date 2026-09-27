// Package alarmeval implements the CloudWatch-style metric-alarm evaluation
// shared by the AWS, Azure, and GCP monitoring providers: exact metric-series
// dimension matching, per-statistic aggregation of the three PutMetricData datum
// forms, and the per-Period M-of-N rule with OK recovery and TreatMissingData
// handling. Keeping this in one place stops the three providers from drifting.
//
// A CloudWatch alarm (Params.ExtendedRange) looks back over an evaluation
// range that is longer than EvaluationPeriods, so a few missing recent points
// do not hide older real ones. The range length is a calibrated
// approximation, see evaluationRangeExtra. A wall clock window
// (Params.WallClock) has no range and looks back exactly EvaluationPeriods.
//
// Evaluation is lazy and clock based. A provider evaluates an alarm when data
// arrives, when the alarm is created or updated, and on any read that shows
// state once EvaluationInterval has passed since the last evaluation. There is
// no background ticker.
//
// By default the window slides. It ends at the evaluation instant and is not
// aligned to the wall clock, which is the documented CloudWatch default: "the
// boundaries of the window are not aligned to the wall clock"
// (https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarm-evaluation.html).
// Params.WallClock aligns the periods to the clock instead, see wall_clock.go.
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
// the empty string, is the AWS default "missing".
const (
	TreatMissingIgnore       = "ignore"
	treatMissing             = "missing"
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
	// ExtendedRange turns on the CloudWatch evaluation range and the
	// premature-alarm rule. Only the AWS provider sets it. Azure evaluates just
	// its windowSize and GCP just its duration, so they look back exactly
	// EvaluationPeriods.
	ExtendedRange bool
	// Band is the anomaly band of a band-operator alarm, bucketed like the
	// datums. A period with data but no band point counts as missing.
	Band []BandPoint
	// ExtendedStatistic, when set, is evaluated in place of Stat, for example
	// p99 or tm90. A period where it is not available counts as missing.
	ExtendedStatistic string
	// LowSampleIgnore is EvaluateLowSampleCountPercentile=ignore: a
	// percentile alarm keeps its state while an evaluated period has too few
	// samples, see LowSample.
	LowSampleIgnore bool
	// WallClock aligns each period to the clock in Location, which is UTC
	// when nil. It turns off the evaluation range of ExtendedRange.
	WallClock bool
	Location  *time.Location
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
// a provider can pre-filter its stored datums. With ExtendedRange it is the
// start of the evaluation range, which reaches back past EvaluationPeriods in
// case recent points are missing.
func (p *Params) WindowStart(now time.Time) time.Time {
	loc := p.locate(now)

	return loc.start(loc.span - 1)
}

// EvaluatedStart is the start of the oldest period an evaluation at now uses.
// That is EvaluationPeriods back, or further when the evaluation reaches back
// into the range for older real points.
func EvaluatedStart(datums []driver.MetricDatum, p *Params, now time.Time) time.Time {
	_, evalPeriods, _ := p.normalize()

	oldest := evalPeriods - 1
	if pts := evaluated(p.realPoints(datums, now), evalPeriods); len(pts) > 0 && pts[0].age > oldest {
		oldest = pts[0].age
	}

	return p.locate(now).start(oldest)
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

// StatOf aggregates every datum in the slice and returns the requested
// statistic, standard or extended, or 0 when it is not available. It folds a
// plain Value, a StatisticValues set, and paired Values/Counts arrays
// uniformly.
func StatOf(datums []driver.MetricDatum, stat string) float64 {
	v, _ := StatValue(datums, stat)

	return v
}

// evaluationRangeExtra is how many periods past EvaluationPeriods an
// evaluation looks back. The guide says CloudWatch "attempts to retrieve a
// higher number of data points than the number specified as Evaluation
// Periods" and that the exact number "depends on the length of the alarm
// period and whether it is based on a metric with standard resolution or high
// resolution". That rule is not published. Its only worked examples use a range
// of 5 for 3 evaluation periods, so cloudemu always looks back N+2 periods.
const evaluationRangeExtra = 2

// span is the number of periods an evaluation looks back over: the
// evaluation range with ExtendedRange, otherwise EvaluationPeriods.
func (p *Params) span() int {
	_, evalPeriods, _ := p.normalize()
	if p.extendedRange() {
		return evalPeriods + evaluationRangeExtra
	}

	return evalPeriods
}

// extendedRange reports whether the CloudWatch evaluation range and the
// premature rule apply. A wall clock window looks back exactly N periods.
func (p *Params) extendedRange() bool {
	return p.ExtendedRange && !p.WallClock
}

// locator places timestamps in the periods of one evaluation. Period 0 is the
// most recent. A sliding period i holds (now-(i+1)p, now-ip]. A wall clock
// period i holds [edges[i+1], edges[i]).
type locator struct {
	now    time.Time
	period time.Duration
	span   int
	edges  []time.Time
}

func (p *Params) locate(now time.Time) *locator {
	periodDur, _, _ := p.normalize()
	l := &locator{now: now, period: periodDur, span: p.span()}

	if p.WallClock {
		zone := p.Location
		if zone == nil {
			zone = time.UTC
		}

		l.edges = wallEdges(now, periodDur, l.span, zone)
	}

	return l
}

// index is the period of ts. ok is false for a time outside the span.
func (l *locator) index(ts time.Time) (int, bool) {
	if l.edges != nil {
		return wallIndex(l.edges, ts)
	}

	return bucketIndex(ts, l.now, l.period, l.span)
}

// start is the start of period i.
func (l *locator) start(i int) time.Time {
	if l.edges != nil {
		return l.edges[i+1]
	}

	return l.now.Add(-l.period * time.Duration(i+1))
}

// slot is one real datapoint of the evaluation range. age 0 is the most
// recent period. samples is its SampleCount.
type slot struct {
	age     int
	value   float64
	breach  bool
	samples float64
}

// extStat is the parsed ExtendedStatistic, or nil when the alarm uses Stat.
// A value that does not parse also gives nil, so the alarm falls back to Stat.
func (p *Params) extStat() *ExtStat {
	if p.ExtendedStatistic == "" {
		return nil
	}

	e, err := ParseExtendedStatistic(p.ExtendedStatistic)
	if err != nil {
		return nil
	}

	return &e
}

// realPoints returns the periods of the evaluation range that hold a usable
// datapoint, newest first.
func (p *Params) realPoints(datums []driver.MetricDatum, now time.Time) []slot {
	loc := p.locate(now)
	ext := p.extStat()
	buckets := bucketByPeriod(datums, loc, ext != nil)
	band := p.bandBuckets(loc)

	var out []slot

	for i, b := range buckets {
		if b == nil {
			continue
		}

		value, ok := b.value(p.Stat, ext)
		if !ok {
			continue
		}

		if has, breach := p.judge(value, band[i]); has {
			out = append(out, slot{age: i, value: value, breach: breach, samples: b.count})
		}
	}

	return out
}

// lowSampleRetain reports whether EvaluateLowSampleCountPercentile=ignore
// keeps the state because an evaluated period has too few samples for the
// percentile.
func (p *Params) lowSampleRetain(points []slot, evalPeriods int) bool {
	if !p.LowSampleIgnore {
		return false
	}

	ext := p.extStat()
	if ext == nil {
		return false
	}

	frac, ok := ext.percentileFraction()
	if !ok {
		return false
	}

	for _, pt := range evaluated(points, evalPeriods) {
		if LowSample(frac, pt.samples) {
			return true
		}
	}

	return false
}

// treatment is the TreatMissingData policy in force. An empty or unknown
// policy is the AWS default "missing".
//
// The AWS docs disagree on AWS/DynamoDB alarms. The user guide page
// alarms-and-missing-data.html says they "default to ignore missing data" and
// "You can override this". The TreatMissingData field of API_PutMetricAlarm
// says they "always ignore missing data even if you choose a different
// option". This follows the user guide, so an explicit policy wins.
func (p *Params) treatment() string {
	switch p.TreatMissingData {
	case TreatMissingIgnore, treatMissingBreaching, treatMissingNotBreaching:
		return p.TreatMissingData
	case "":
		if p.IgnoreMissingByDefault {
			return TreatMissingIgnore
		}
	}

	return treatMissing
}

// EvaluateWindow applies CloudWatch's M-of-N rule over the evaluation range,
// following "How alarm state is evaluated when data is missing" in
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/alarms-and-missing-data.html.
// Datums are already filtered to the alarm's metric series and to WindowStart.
//
// With at least EvaluationPeriods real points in the range, "CloudWatch
// evaluates the alarm state based on the most recent real data points" and
// TreatMissingData "is not needed and is ignored". With fewer, it "fills in
// the missing data points with the result you specified" and "all real data
// points in the evaluation range are included". The alarm is in ALARM when at
// least DatapointsToAlarm of the points breach and OK otherwise.
//
// Without ExtendedRange the evaluation sees exactly EvaluationPeriods periods
// and the premature rule is off.
func EvaluateWindow(datums []driver.MetricDatum, p *Params, now time.Time) Outcome {
	_, evalPeriods, datapointsToAlarm := p.normalize()
	points := p.realPoints(datums, now)

	if p.lowSampleRetain(points, evalPeriods) {
		return Outcome{Retain: true}
	}

	if len(points) >= evalPeriods {
		return thresholdOutcome(countBreaching(points[:evalPeriods]), datapointsToAlarm)
	}

	treat := p.treatment()
	fill := evalPeriods - len(points)
	breaching := countBreaching(points)

	switch {
	case len(points) == 0 && (treat == treatMissing || treat == TreatMissingIgnore):
		return missingOutcome(treat, evalPeriods)
	case treat == treatMissingBreaching:
		breaching += fill
	case treat == treatMissingNotBreaching:
		// Filled points are good, so nothing is missing and the premature
		// rule does not apply.
	case p.prematureApplies(points, breaching, datapointsToAlarm):
		// "the alarm goes into ALARM state even if missing data points are
		// treated as missing." With "ignore" the doc tables keep the state.
		if treat == TreatMissingIgnore {
			return Outcome{Retain: true}
		}

		return Outcome{State: StateAlarm, Reason: "Threshold crossed"}
	}

	return thresholdOutcome(breaching, datapointsToAlarm)
}

// prematureApplies reports whether the premature rule decides a CloudWatch
// evaluation. Enough real breaching points alarm first, whatever the policy.
func (p *Params) prematureApplies(points []slot, breaching, datapointsToAlarm int) bool {
	return p.extendedRange() && breaching < datapointsToAlarm && premature(points, datapointsToAlarm)
}

// premature is the rule from "Avoiding premature transitions to alarm state":
// "alarms are designed to always go into ALARM state when the oldest available
// breaching datapoint during the Evaluation Periods number of data points is
// at least as old as the value of Datapoints to Alarm. All other more recent
// data points are breaching or missing." points holds every real point in the
// range, newest first.
//
// The doc tables add one more condition. The 2 out of 3 row "0 - X - -" stays
// OK although its breaching point is old enough, so a non-breaching point
// anywhere in the range turns the rule off.
func premature(points []slot, datapointsToAlarm int) bool {
	if len(points) == 0 {
		return false
	}

	for _, pt := range points {
		if !pt.breach {
			return false
		}
	}

	oldest := points[len(points)-1]

	return oldest.age+1 >= datapointsToAlarm
}

func countBreaching(points []slot) int {
	n := 0

	for _, pt := range points {
		if pt.breach {
			n++
		}
	}

	return n
}

func thresholdOutcome(breaching, datapointsToAlarm int) Outcome {
	if breaching >= datapointsToAlarm {
		return Outcome{State: StateAlarm, Reason: "Threshold crossed"}
	}

	return Outcome{State: StateOK, Reason: "Threshold not crossed"}
}

// judge reports whether a period's value is a usable datapoint and whether
// it breaches. A band alarm needs a band point too.
func (p *Params) judge(value float64, band *BandPoint) (has, breach bool) {
	if !IsBandOperator(p.ComparisonOperator) {
		return true, EvaluateComparison(value, p.ComparisonOperator, p.Threshold)
	}

	if band == nil {
		return false, false
	}

	return true, breachesBand(value, p.ComparisonOperator, *band)
}

// bandBuckets places each band point in its period, like bucketByPeriod. The
// latest point wins when a period has more than one. A nil entry has none.
func (p *Params) bandBuckets(loc *locator) []*BandPoint {
	out := make([]*BandPoint, loc.span)

	for i := range p.Band {
		bp := &p.Band[i]

		idx, ok := loc.index(bp.Timestamp)
		if !ok {
			continue
		}

		if out[idx] == nil || bp.Timestamp.After(out[idx].Timestamp) {
			out[idx] = bp
		}
	}

	return out
}

// RecentBand returns the band edges of each evaluated period, oldest first.
// These are the periods RecentDatapoints lists. CloudWatch reports them in the
// stateReasonData of an anomaly alarm.
func RecentBand(datums []driver.MetricDatum, p *Params, now time.Time) (lower, upper []float64) {
	_, evalPeriods, _ := p.normalize()
	band := p.bandBuckets(p.locate(now))

	lower, upper = []float64{}, []float64{}

	for _, pt := range evaluated(p.realPoints(datums, now), evalPeriods) {
		if band[pt.age] == nil {
			continue
		}

		lower = append(lower, band[pt.age].Lower)
		upper = append(upper, band[pt.age].Upper)
	}

	return lower, upper
}

// evaluated returns the real points an evaluation uses, oldest first: the
// most recent evalPeriods of them, or all of them when there are fewer.
func evaluated(points []slot, evalPeriods int) []slot {
	if len(points) > evalPeriods {
		points = points[:evalPeriods]
	}

	out := make([]slot, len(points))
	for i, pt := range points {
		out[len(points)-1-i] = pt
	}

	return out
}

// missingOutcome is the result when every period in the evaluation range is
// empty. "missing" gives INSUFFICIENT_DATA and "ignore" keeps the state.
func missingOutcome(treat string, evalPeriods int) Outcome {
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

// RecentDatapoints returns the statistic of each evaluated period, oldest
// first. These are the most recent real points of the evaluation range, up to
// EvaluationPeriods of them. CloudWatch reports these in the stateReasonData of
// a metric-driven transition.
func RecentDatapoints(datums []driver.MetricDatum, p *Params, now time.Time) []float64 {
	_, evalPeriods, _ := p.normalize()
	points := evaluated(p.realPoints(datums, now), evalPeriods)

	out := make([]float64, 0, len(points))
	for _, pt := range points {
		out = append(out, pt.value)
	}

	return out
}

// bucketByPeriod groups datums into span accumulators indexed by age, where
// bucket 0 covers the most recent period. A nil bucket had no data. keep
// retains the raw values an extended statistic needs.
func bucketByPeriod(datums []driver.MetricDatum, loc *locator, keep bool) []*statAgg {
	buckets := make([]*statAgg, loc.span)

	for i := range datums {
		idx, ok := loc.index(datums[i].Timestamp)
		if !ok {
			continue
		}

		if buckets[idx] == nil {
			buckets[idx] = &statAgg{keep: keep}
		}

		foldDatum(buckets[idx], &datums[i])
	}

	return buckets
}

// bucketIndex is the age bucket of ts, where 0 is the most recent period.
// ok is false for a future time or one older than the span.
func bucketIndex(ts, now time.Time, periodDur time.Duration, span int) (int, bool) {
	age := now.Sub(ts)
	if age < 0 {
		return 0, false
	}

	idx := int(age / periodDur)
	if idx >= span {
		return 0, false
	}

	return idx, true
}

// statAgg accumulates SampleCount / Sum / Minimum / Maximum across a set of
// metric datums so any requested statistic can be derived. It treats a plain
// Value, a pre-aggregated StatisticValues set, and paired Values/Counts arrays
// uniformly, matching how real CloudWatch folds all three into one series.
//
// With keep set it also retains each raw value for the extended statistics.
// A statistic set hides its raw values, so it makes them unavailable unless
// it stands for one value: SampleCount 1, or Minimum equal to Maximum.
type statAgg struct {
	count float64
	sum   float64
	min   float64
	max   float64
	seen  bool

	keep     bool
	obs      []observation
	unusable bool
}

// observe retains one raw value with its count.
func (a *statAgg) observe(value, weight float64) {
	if a.keep && weight > 0 {
		a.obs = append(a.obs, observation{value: value, weight: weight})
	}
}

// observeSet retains the raw value a statistic set stands for, if it has one.
func (a *statAgg) observeSet(s *driver.StatisticSet) {
	switch {
	case s.SampleCount <= 0:
	case s.Minimum == s.Maximum:
		a.observe(s.Minimum, s.SampleCount)
	case s.SampleCount == 1:
		a.observe(s.Sum, 1)
	default:
		a.unusable = true
	}
}

// extStat returns the extended statistic over the retained values.
func (a *statAgg) extStat(e *ExtStat) (float64, bool) {
	if !a.seen || a.unusable {
		return 0, false
	}

	return e.compute(a.obs)
}

// value is the statistic an evaluation compares: ext when set, else stat.
func (a *statAgg) value(stat string, ext *ExtStat) (float64, bool) {
	if ext != nil {
		return a.extStat(ext)
	}

	return a.stat(stat), a.seen
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

// stat returns the requested standard statistic, or 0 when no data was
// accumulated. An empty or unknown stat is Average. Extended statistics go
// through extStat.
func (a *statAgg) stat(stat string) float64 {
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
	default: // "Average" or unspecified
		return a.sum / a.count
	}
}

// aggregate folds every datum into a single accumulator. keep retains the
// raw values an extended statistic needs.
func aggregate(datums []driver.MetricDatum, keep bool) *statAgg {
	a := &statAgg{keep: keep}

	for i := range datums {
		foldDatum(a, &datums[i])
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
		a.observeSet(s)
	case len(d.Values) > 0:
		for j, v := range d.Values {
			count := 1.0
			if j < len(d.Counts) {
				count = d.Counts[j]
			}

			a.add(count, v*count, v, v)
			a.observe(v, count)
		}
	default:
		a.add(1, d.Value, d.Value, d.Value)
		a.observe(d.Value, 1)
	}
}
