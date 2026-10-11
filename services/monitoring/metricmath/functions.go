package metricmath

import "time"

// This file holds IF and FILL. Their rules follow the CloudWatch user guide,
// "Using IF expressions" and the FILL row of the function table:
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/using-metric-math.html

// ifNode is IF(condition, trueValue[, falseValue]).
type ifNode struct {
	cond, then, otherwise node
}

func (n ifNode) evaluate(s scope) (result, error) {
	cond, err := n.cond.evaluate(s)
	if err != nil {
		return result{}, err
	}

	then, err := n.then.evaluate(s)
	if err != nil {
		return result{}, err
	}

	var otherwise *result

	if n.otherwise != nil {
		v, err := n.otherwise.evaluate(s)
		if err != nil {
			return result{}, err
		}

		otherwise = &v
	}

	// IF returns a single time series. An array in any argument is not one
	// of the documented forms.
	if cond.isArray || then.isArray || (otherwise != nil && otherwise.isArray) {
		return result{series: emptySeries()}, nil
	}

	if cond.isScalar {
		return ifScalar(cond.scalar, &then, otherwise), nil
	}

	return result{series: ifSeries(cond.series, &then, otherwise)}, nil
}

// ifScalar is IF(S, a, b): a when S is true, b when false, and an empty
// series when false and b is left out.
func ifScalar(cond float64, then, otherwise *result) result {
	if cond != 0 {
		return *then
	}

	if otherwise == nil {
		return result{series: emptySeries()}
	}

	return *otherwise
}

// ifSeries is IF(TS, a, b), point by point over the condition series:
//
//   - A missing condition point gives no output point.
//   - A true point takes a. A false point takes b, and gives no point when b
//     is left out.
//   - A scalar branch gives its value.
//   - A series branch with no point at that time gives 0, except that a
//     missing false-branch point gives no point when the true branch is a
//     scalar. This is the IF(metric1, scalar2, metric3) table of the guide.
func ifSeries(cond Series, then, otherwise *result) Series {
	thenAt := branchValues(then)

	var otherAt map[int64]float64
	if otherwise != nil {
		otherAt = branchValues(otherwise)
	}

	out := emptySeries()

	for i, ts := range cond.Timestamps {
		var (
			v  float64
			ok bool
		)

		if cond.Values[i] != 0 {
			v, ok = branchValue(then, thenAt, ts, true)
		} else if otherwise != nil {
			v, ok = branchValue(otherwise, otherAt, ts, !then.isScalar)
		}

		if ok {
			out.Timestamps = append(out.Timestamps, ts)
			out.Values = append(out.Values, v)
		}
	}

	return out
}

func branchValues(r *result) map[int64]float64 {
	if r.isScalar {
		return nil
	}

	return valuesAt(r.series)
}

// branchValue reads one IF branch at ts. zeroIfMissing says whether a series
// branch with no point there gives 0 or no point.
func branchValue(r *result, at map[int64]float64, ts time.Time, zeroIfMissing bool) (float64, bool) {
	if r.isScalar {
		return r.scalar, true
	}

	if v, ok := at[ts.UnixNano()]; ok {
		return v, true
	}

	return 0, zeroIfMissing
}

// maxFillPoints caps the points FILL creates for one series. It is the
// GetMetricData datapoint budget of one call.
const maxFillPoints = 100800

// fillNode is FILL(input, filler). The filler is a scalar, a series, REPEAT
// or LINEAR.
type fillNode struct {
	input, with node
}

func (n fillNode) evaluate(s scope) (result, error) {
	in, err := n.input.evaluate(s)
	if err != nil {
		return result{}, err
	}

	if in.isScalar {
		return in, nil
	}

	f, err := n.filler(s)
	if err != nil {
		return result{}, err
	}

	grid := s.e.grid(s.gridPeriod)

	if in.isArray {
		return mapArray(in.array, func(r result) result { return result{series: f.apply(r.series, grid)} }), nil
	}

	return result{series: f.apply(in.series, grid)}, nil
}

func (n fillNode) filler(s scope) (filler, error) {
	if kw, ok := n.with.(keywordNode); ok {
		return filler{mode: kw.word}, nil
	}

	v, err := n.with.evaluate(s)
	if err != nil {
		return filler{}, err
	}

	switch {
	case v.isScalar:
		return filler{value: &v.scalar}, nil
	case v.isArray:
		// The filler of FILL is a scalar, one series or a keyword.
		return filler{none: true}, nil
	default:
		return filler{series: valuesAt(v.series)}, nil
	}
}

// filler is the value source of one FILL.
type filler struct {
	mode   string // kwRepeat or kwLinear, else empty
	value  *float64
	series map[int64]float64
	none   bool
}

// grid returns the timestamps of every period in the evaluator's range,
// aligned to its start as the metric reads are. It is nil when the range or
// the period is unknown, or the range holds more than maxFillPoints periods.
func (e *Evaluator) grid(period int) []time.Time {
	if period <= 0 || e.start.IsZero() || !e.end.After(e.start) {
		return nil
	}

	step := time.Duration(period) * time.Second
	if e.end.Sub(e.start)/step > maxFillPoints {
		return nil
	}

	var out []time.Time

	for ts := e.start; ts.Before(e.end); ts = ts.Add(step) {
		out = append(out, ts)
	}

	return out
}

// apply fills the points of grid that s has no value at. The points of s
// are kept as they are.
func (f filler) apply(s Series, grid []time.Time) Series {
	if f.none || len(grid) == 0 {
		return s
	}

	known := valuesAt(s)
	stamps := mergeTimestamps(s.Timestamps, grid)

	switch f.mode {
	case kwRepeat:
		return fillRepeat(known, stamps)
	case kwLinear:
		return fillLinear(known, stamps)
	}

	out := emptySeries()

	for _, ts := range stamps {
		v, ok := known[ts.UnixNano()]
		if !ok {
			v, ok = f.fixed(ts)
		}

		if ok {
			out.Timestamps = append(out.Timestamps, ts)
			out.Values = append(out.Values, v)
		}
	}

	return out
}

// fixed is the scalar or series filler value at ts.
func (f filler) fixed(ts time.Time) (float64, bool) {
	if f.value != nil {
		return *f.value, true
	}

	v, ok := f.series[ts.UnixNano()]

	return v, ok
}

// fillRepeat fills a missing point with the latest actual value before it.
// A gap before the first actual value has nothing to repeat and stays empty.
func fillRepeat(known map[int64]float64, stamps []time.Time) Series {
	out := emptySeries()

	var (
		last    float64
		hasLast bool
	)

	for _, ts := range stamps {
		if v, ok := known[ts.UnixNano()]; ok {
			last, hasLast = v, true
		}

		if hasLast {
			out.Timestamps = append(out.Timestamps, ts)
			out.Values = append(out.Values, last)
		}
	}

	return out
}

// fillLinear fills a missing point on the straight line between the actual
// values at the start and the end of its gap. A gap before the first or
// after the last actual value has no end to interpolate to and stays empty.
func fillLinear(known map[int64]float64, stamps []time.Time) Series {
	out := emptySeries()
	prev := -1

	for i, ts := range stamps {
		v, ok := known[ts.UnixNano()]
		if !ok {
			continue
		}

		if prev >= 0 {
			p := stamps[prev]
			pv := known[p.UnixNano()]
			span := ts.Sub(p).Seconds()

			for _, gap := range stamps[prev+1 : i] {
				out.Timestamps = append(out.Timestamps, gap)
				out.Values = append(out.Values, pv+(v-pv)*gap.Sub(p).Seconds()/span)
			}
		}

		out.Timestamps = append(out.Timestamps, ts)
		out.Values = append(out.Values, v)
		prev = i
	}

	return out
}
