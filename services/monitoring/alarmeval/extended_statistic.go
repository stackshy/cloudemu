package alarmeval

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// The extended statistics of
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/Statistics-definitions.html.
// All of them need raw data points and none is available when any value is
// negative.
//
// A percentile uses the nearest-rank method. CloudWatch does not publish its
// exact estimator, and nearest rank gives the documented answer on every
// example: p90 of 1..100 is 90.

// extKind is the family of an extended statistic.
type extKind int

const (
	extPercentile extKind = iota
	extTrimmedMean
	extWinsorizedMean
	extTrimmedCount
	extTrimmedSum
	extPercentileRank
)

// hundred is the top of a percentage.
const hundred = 100.0

// ExtStat is a parsed extended statistic such as p99, tm90 or TM(10%:90%).
type ExtStat struct {
	kind extKind
	// pct is the percentile of a pNN statistic.
	pct float64
	// lo and hi bound the range of a trimmed statistic. percent says whether
	// they are percentiles or absolute values. An unset bound is open.
	lo, hi       float64
	loSet, hiSet bool
	percent      bool
}

var (
	errExtStat = errors.New("not a valid extended statistic")

	percentilePattern = regexp.MustCompile(`^p(\d+(?:\.\d{1,10})?)$`)
	shortPattern      = regexp.MustCompile(`^(tm|wm|tc|ts)(\d+(?:\.\d{1,10})?)$`)
	rangePattern      = regexp.MustCompile(`^(TM|WM|TC|TS|PR)\(([^:()]*):([^:()]*)\)$`)
	boundPattern      = regexp.MustCompile(`^(\d+(?:\.\d{1,10})?)(%?)$`)
)

// rangeKinds maps the upper-case range prefixes to their kind.
//
//nolint:gochecknoglobals // closed lookup table
var rangeKinds = map[string]extKind{
	"TM": extTrimmedMean, "WM": extWinsorizedMean, "TC": extTrimmedCount,
	"TS": extTrimmedSum, "PR": extPercentileRank,
}

// shortKinds maps the lower-case one-number prefixes to their kind.
//
//nolint:gochecknoglobals // closed lookup table
var shortKinds = map[string]extKind{
	"tm": extTrimmedMean, "wm": extWinsorizedMean, "tc": extTrimmedCount, "ts": extTrimmedSum,
}

// ParseExtendedStatistic parses the extended statistic grammar: pNN, the
// one-number forms tmNN, wmNN, tcNN and tsNN, IQM, and the range forms
// TM(a:b), WM(a:b), TC(a:b), TS(a:b) and PR(a:b). A range bound is a
// percentage such as 10% or an absolute value, and either bound may be left
// out. PR takes absolute values only.
func ParseExtendedStatistic(s string) (ExtStat, error) {
	if s == "IQM" {
		return ExtStat{kind: extTrimmedMean, lo: 25, hi: 75, loSet: true, hiSet: true, percent: true}, nil
	}

	if m := percentilePattern.FindStringSubmatch(s); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		if v > hundred {
			return ExtStat{}, errExtStat
		}

		return ExtStat{kind: extPercentile, pct: v}, nil
	}

	if m := shortPattern.FindStringSubmatch(s); m != nil {
		v, _ := strconv.ParseFloat(m[2], 64)
		if v <= 0 || v > hundred {
			return ExtStat{}, errExtStat
		}

		return ExtStat{kind: shortKinds[m[1]], hi: v, hiSet: true, percent: true}, nil
	}

	if m := rangePattern.FindStringSubmatch(s); m != nil {
		return parseRange(rangeKinds[m[1]], m[2], m[3])
	}

	return ExtStat{}, errExtStat
}

// parseRange parses the two bounds of a range statistic.
func parseRange(kind extKind, loStr, hiStr string) (ExtStat, error) {
	e := ExtStat{kind: kind}

	lo, loPct, loSet, err := parseBound(loStr)
	if err != nil {
		return ExtStat{}, err
	}

	hi, hiPct, hiSet, err := parseBound(hiStr)
	if err != nil {
		return ExtStat{}, err
	}

	e.lo, e.loSet, e.hi, e.hiSet = lo, loSet, hi, hiSet
	e.percent = loPct || hiPct

	if !e.boundsValid(loPct, hiPct) {
		return ExtStat{}, errExtStat
	}

	return e, nil
}

// boundsValid checks a range: at least one bound, both of one kind, lower
// below upper, and percentages up to 100. PR takes absolute values only.
func (e *ExtStat) boundsValid(loPct, hiPct bool) bool {
	both := e.loSet && e.hiSet

	switch {
	case !e.loSet && !e.hiSet:
		return false
	case both && (loPct != hiPct || e.lo >= e.hi):
		return false
	case !e.percent:
		return true
	default:
		return e.kind != extPercentileRank && e.lo <= hundred && e.hi <= hundred
	}
}

// parseBound parses one range bound. An empty bound is open.
func parseBound(s string) (v float64, percent, set bool, err error) {
	if s == "" {
		return 0, false, false, nil
	}

	m := boundPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false, false, errExtStat
	}

	v, _ = strconv.ParseFloat(m[1], 64)

	return v, m[2] == "%", true, nil
}

// IsPercentile reports whether s is a pNN percentile, the only extended
// statistic GetMetricStatistics accepts.
func IsPercentile(s string) bool {
	e, err := ParseExtendedStatistic(s)

	return err == nil && e.kind == extPercentile
}

// Compute returns the statistic over raw values. ok is false when there is no
// data, when a value is negative, or when a trimmed mean keeps no value.
func (e *ExtStat) Compute(values []float64) (float64, bool) {
	obs := make([]observation, 0, len(values))
	for _, v := range values {
		obs = append(obs, observation{value: v, weight: 1})
	}

	return e.compute(obs)
}

// observation is one raw value with its count.
type observation struct {
	value  float64
	weight float64
}

func (e *ExtStat) compute(obs []observation) (float64, bool) {
	total := 0.0

	for _, o := range obs {
		if o.value < 0 {
			return 0, false
		}

		total += o.weight
	}

	if total <= 0 {
		return 0, false
	}

	sorted := append([]observation(nil), obs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].value < sorted[j].value })

	return e.over(sorted, total)
}

// over computes the statistic over sorted, non-negative observations whose
// weights add up to total.
func (e *ExtStat) over(sorted []observation, total float64) (float64, bool) {
	switch e.kind {
	case extPercentile:
		return rankValue(sorted, total, e.pct), true
	case extWinsorizedMean:
		return e.winsorized(sorted, total), true
	case extPercentileRank:
		_, count := e.trimmed(sorted, total)

		return hundred * count / total, true
	case extTrimmedCount:
		_, count := e.trimmed(sorted, total)

		return count, true
	case extTrimmedSum:
		sum, _ := e.trimmed(sorted, total)

		return sum, true
	case extTrimmedMean:
		sum, count := e.trimmed(sorted, total)
		if count <= 0 {
			return 0, false
		}

		return sum / count, true
	}

	return 0, false
}

// rankValue is the nearest-rank percentile pct of sorted, whose weights add
// up to total.
func rankValue(sorted []observation, total, pct float64) float64 {
	rank := math.Max(1, math.Ceil(pct/hundred*total))
	cum := 0.0

	for _, o := range sorted {
		cum += o.weight
		if cum >= rank {
			return o.value
		}
	}

	return sorted[len(sorted)-1].value
}

// trimmed returns the sum and count of the values inside the range. A
// percentage range keeps the ranks in (lo%, hi%] of the data, so a value that
// straddles a cut counts in part. An absolute range keeps lo < v <= hi.
func (e *ExtStat) trimmed(sorted []observation, total float64) (sum, count float64) {
	if !e.percent {
		for _, o := range sorted {
			if e.inside(o.value) {
				sum += o.value * o.weight
				count += o.weight
			}
		}

		return sum, count
	}

	lowCut, highCut := e.cuts(total)
	cum := 0.0

	for _, o := range sorted {
		from, to := cum, cum+o.weight
		cum = to

		kept := math.Min(to, highCut) - math.Max(from, lowCut)
		if kept > 0 {
			sum += o.value * kept
			count += kept
		}
	}

	return sum, count
}

// cuts are the ranks a percentage range keeps: (lowCut, highCut].
func (e *ExtStat) cuts(total float64) (lowCut, highCut float64) {
	lowCut, highCut = 0, total

	if e.loSet {
		lowCut = e.lo / hundred * total
	}

	if e.hiSet {
		highCut = e.hi / hundred * total
	}

	return lowCut, highCut
}

// inside reports whether v is in an absolute range: lo < v <= hi.
func (e *ExtStat) inside(v float64) bool {
	return (!e.loSet || v > e.lo) && (!e.hiSet || v <= e.hi)
}

// winsorized is the mean after values outside the range are moved to its
// edge. The edges of a percentage range are the values at those percentiles.
func (e *ExtStat) winsorized(sorted []observation, total float64) float64 {
	low, high := math.Inf(-1), math.Inf(1)

	switch {
	case e.percent:
		if e.loSet && e.lo > 0 {
			low = rankValue(sorted, total, e.lo)
		}

		if e.hiSet && e.hi < hundred {
			high = rankValue(sorted, total, e.hi)
		}
	default:
		if e.loSet {
			low = e.lo
		}

		if e.hiSet {
			high = e.hi
		}
	}

	sum := 0.0
	for _, o := range sorted {
		sum += math.Min(math.Max(o.value, low), high) * o.weight
	}

	return sum / total
}

// percentileFraction returns the percentile of a pNN statistic as a fraction.
func (e *ExtStat) percentileFraction() (float64, bool) {
	if e.kind != extPercentile {
		return 0, false
	}

	return e.pct / hundred, true
}

// lowSampleFactor is the "10" of the low-sample rule and lowSampleMid the
// percentile where its formula changes.
const (
	lowSampleFactor = 10.0
	lowSampleMid    = 0.5
)

// LowSample reports whether n samples are too few for percentile p, given as
// a fraction. From
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/percentiles-with-low-samples.html:
// for p in [0.5, 1) that is fewer than 10/(1-p) points, and for p in (0, 0.5)
// fewer than 10/p. p0 and p100 have no such rule.
func LowSample(p, n float64) bool {
	switch {
	case p <= 0 || p >= 1:
		return false
	case p >= lowSampleMid:
		return n < lowSampleFactor/(1-p)
	default:
		return n < lowSampleFactor/p
	}
}

// StatValue aggregates the datums and returns the requested statistic. stat
// is a standard statistic or an extended one. ok is false when there is no
// data or the extended statistic is not available for it. An unknown stat
// reads as Average, as StatOf does.
func StatValue(datums []driver.MetricDatum, stat string) (float64, bool) {
	e, err := ParseExtendedStatistic(stat)
	if err != nil {
		a := aggregate(datums, false)

		return a.stat(stat), a.seen
	}

	a := aggregate(datums, true)

	return a.extStat(&e)
}
