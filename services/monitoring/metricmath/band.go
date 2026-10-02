package metricmath

import (
	"math"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// ANOMALY_DETECTION_BAND(id, k) returns two series, the lower and the upper
// edge of the expected range of entry id. AWS computes them with a trained
// machine learning model. This package uses a documented approximation:
//
//   - The band has a point at each point t of the input series.
//   - The training points are the input's points in [t-TrainingWindow, t),
//     minus any point whose period overlaps an excluded time range.
//   - With fewer than MinTrainingPoints training points there is no band
//     point at t.
//   - Otherwise the band is mean -/+ k standard deviations of the training
//     points. There is no seasonality and no trend.

// TrainingWindow is how far back the band looks. AWS trains on up to two
// weeks of data.
const TrainingWindow = 14 * 24 * time.Hour

// MinTrainingPoints is the least number of points a model needs.
const MinTrainingPoints = 3

// bandFloor is the least half width of a band, relative to its center. It
// absorbs rounding in the mean, so a flat series never breaches its own band.
const bandFloor = 1e-9

// Band is the lower and upper edge of an anomaly band, one pair per timestamp.
type Band struct {
	Timestamps []time.Time
	Lower      []float64
	Upper      []float64
}

// BandConfig tells an evaluator where the band's training data comes from.
type BandConfig struct {
	// History reads a metric over the evaluator's range extended back by
	// TrainingWindow. Nil trains on the points in the range only.
	History Fetcher
	// Excluded returns the time ranges left out of training for the entry
	// inputID of queries. Nil excludes nothing.
	Excluded func(queries []driver.MetricDataQuery, inputID string) []driver.TimeRange
}

// Trained reports whether a model with this many points can produce a band.
func Trained(points int) bool {
	return points >= MinTrainingPoints
}

// BandInput returns the entry ID that an ANOMALY_DETECTION_BAND expression
// reads. ok is false when expr is not a band call.
func BandInput(expr string) (id string, ok bool) {
	n, ok := parseBand(tokenizeMath(expr))
	if !ok {
		return "", false
	}

	return n.input, true
}

// WithBand sets where band training data comes from and returns e.
func (e *Evaluator) WithBand(cfg BandConfig) *Evaluator {
	e.band = cfg
	e.history = nil

	return e
}

// Band returns the band of entry id at the input's own period. ok is false
// when id is not an ANOMALY_DETECTION_BAND entry.
func (e *Evaluator) Band(id string) (Band, bool, error) {
	return e.BandAt(id, 0)
}

// BandAt is Band with the input read at period seconds. An entry with its
// own Period uses that instead.
func (e *Evaluator) BandAt(id string, period int) (Band, bool, error) {
	q, ok := e.byID[id]
	if !ok {
		return Band{}, false, nil
	}

	n, ok := parseBand(tokenizeMath(q.Expression))
	if !ok {
		return Band{}, false, nil
	}

	if q.Period > 0 {
		period = q.Period
	}

	// The input's own period sets how long each training point lasts.
	pointPeriod := period
	if pointPeriod == 0 {
		if in, ok := e.byID[n.input]; ok {
			pointPeriod = Period(e.queries, in)
		}
	}

	target, err := e.resolve(n.input, period)
	if err != nil {
		return Band{}, true, err
	}

	history := target

	if e.band.History != nil {
		history, err = e.historyEvaluator().resolve(n.input, period)
		if err != nil {
			return Band{}, true, err
		}
	}

	var excluded []driver.TimeRange
	if e.band.Excluded != nil {
		excluded = e.band.Excluded(e.queries, n.input)
	}

	return computeBand(target, history, n.k, excludedPeriods{ranges: excluded, period: pointPeriod}), true, nil
}

// historyEvaluator resolves the same queries over the training range.
func (e *Evaluator) historyEvaluator() *Evaluator {
	if e.history == nil {
		e.history = New(e.queries, e.band.History)
	}

	return e.history
}

type timedValue struct {
	ts time.Time
	v  float64
}

// excludedPeriods are the excluded ranges and the period each point covers.
type excludedPeriods struct {
	ranges []driver.TimeRange
	period int
}

// covers reports whether the period that starts at ts overlaps a range. A
// point with no known period is a single instant.
func (x excludedPeriods) covers(ts time.Time) bool {
	end := ts.Add(time.Duration(x.period) * time.Second)

	for _, r := range x.ranges {
		if !ts.After(r.EndTime) && (end.After(r.StartTime) || !ts.Before(r.StartTime)) {
			return true
		}
	}

	return false
}

// trainingPoints returns the history points outside every excluded range,
// sorted by time.
func trainingPoints(history Series, excluded excludedPeriods) []timedValue {
	out := make([]timedValue, 0, len(history.Values))

	for i, ts := range history.Timestamps {
		if ts.IsZero() || excluded.covers(ts) {
			continue
		}

		out = append(out, timedValue{ts: ts, v: history.Values[i]})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })

	return out
}

// bandWindow is a sliding window over sorted training points. It keeps the
// sum and the sum of squares of each value minus shift, so a metric with a
// large mean keeps the precision of its variance.
type bandWindow struct {
	pts    []timedValue
	lo, hi int
	shift  float64
	sum    float64
	sq     float64
}

// moveTo makes the window hold the points in [t-TrainingWindow, t).
func (w *bandWindow) moveTo(t time.Time) {
	for w.hi < len(w.pts) && w.pts[w.hi].ts.Before(t) {
		d := w.pts[w.hi].v - w.shift
		w.sum += d
		w.sq += d * d
		w.hi++
	}

	start := t.Add(-TrainingWindow)

	for w.lo < w.hi && w.pts[w.lo].ts.Before(start) {
		d := w.pts[w.lo].v - w.shift
		w.sum -= d
		w.sq -= d * d
		w.lo++
	}
}

// edges returns the band edges for the points in the window.
func (w *bandWindow) edges(k float64) (lower, upper float64, ok bool) {
	n := w.hi - w.lo
	if !Trained(n) {
		return 0, 0, false
	}

	mean := w.sum / float64(n)
	variance := max(w.sq/float64(n)-mean*mean, 0)
	center := w.shift + mean
	half := max(k*math.Sqrt(variance), bandFloor*math.Abs(center))

	return center - half, center + half, true
}

// computeBand builds the band at each target point in one pass over the
// training points.
func computeBand(target, history Series, k float64, excluded excludedPeriods) Band {
	w := &bandWindow{pts: trainingPoints(history, excluded)}
	if len(w.pts) > 0 {
		w.shift = w.pts[0].v
	}

	order := make([]int, 0, len(target.Timestamps))

	for i, ts := range target.Timestamps {
		if !ts.IsZero() {
			order = append(order, i)
		}
	}

	sort.SliceStable(order, func(i, j int) bool { return target.Timestamps[order[i]].Before(target.Timestamps[order[j]]) })

	out := Band{Timestamps: []time.Time{}, Lower: []float64{}, Upper: []float64{}}

	for _, i := range order {
		t := target.Timestamps[i]
		w.moveTo(t)

		lower, upper, ok := w.edges(k)
		if !ok {
			continue
		}

		out.Timestamps = append(out.Timestamps, t)
		out.Lower = append(out.Lower, lower)
		out.Upper = append(out.Upper, upper)
	}

	return out
}
