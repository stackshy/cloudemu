package metricmath

import (
	"math"
	"slices"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Period rules from the MetricStat, MetricDataQuery and GetMetricStatistics
// API references. Period is an Integer of at least 1. A regular resolution
// period is a multiple of 60, and a high-resolution metric also allows 1, 5,
// 10, 20 and 30.
const (
	maxPeriodSeconds      = math.MaxInt32
	periodSecondsInMinute = 60
)

// ValidPeriod reports whether p is 1, 5, 10, 20, 30 or a multiple of 60 that
// fits the API's Integer type. A valid period never overflows a
// time.Duration once it is multiplied by time.Second.
func ValidPeriod(p int) bool {
	switch {
	case p < 1 || p > maxPeriodSeconds:
		return false
	case p%periodSecondsInMinute == 0:
		return true
	default:
		periods := highResolutionPeriods()

		return slices.Contains(periods[:], p)
	}
}

// highResolutionPeriods are the periods under a minute that a high-resolution
// metric allows.
func highResolutionPeriods() [5]int {
	return [5]int{1, 5, 10, 20, 30}
}

// Period is the granularity of q's points. It is q's own Period, else its
// metric's period, else the largest period of the metrics q reads, else the
// period of the first metric in the list. It is 0 when none is set.
func Period(queries []driver.MetricDataQuery, q *driver.MetricDataQuery) int {
	if q.Period > 0 {
		return q.Period
	}

	if q.MetricStat != nil && q.MetricStat.Period > 0 {
		return q.MetricStat.Period
	}

	if p := referencedPeriod(index(queries), q, map[string]bool{}); p > 0 {
		return p
	}

	for i := range queries {
		if ms := queries[i].MetricStat; ms != nil && ms.Period > 0 {
			return ms.Period
		}
	}

	return 0
}

func index(queries []driver.MetricDataQuery) map[string]*driver.MetricDataQuery {
	byID := make(map[string]*driver.MetricDataQuery, len(queries))
	for i := range queries {
		byID[queries[i].ID] = &queries[i]
	}

	return byID
}

// referencedPeriod is the largest metric period q reads, directly or through
// other expressions.
func referencedPeriod(byID map[string]*driver.MetricDataQuery, q *driver.MetricDataQuery, seen map[string]bool) int {
	if seen[q.ID] {
		return 0
	}

	seen[q.ID] = true

	if q.MetricStat != nil {
		return q.MetricStat.Period
	}

	refs, _ := References(q.Expression)
	best := 0

	for _, id := range refs {
		if ref, ok := byID[id]; ok {
			best = max(best, referencedPeriod(byID, ref, seen))
		}
	}

	return best
}

// Cycle returns the ID of an expression that reads itself, directly or
// through other expressions. ok is false when there is no cycle.
func Cycle(queries []driver.MetricDataQuery) (id string, ok bool) {
	byID := index(queries)
	done := map[string]bool{}

	for i := range queries {
		if onCycle(byID, queries[i].ID, map[string]bool{}, done) {
			return queries[i].ID, true
		}
	}

	return "", false
}

// onCycle walks the references of id. path holds the IDs on the current walk
// and done the IDs already known to be cycle free.
func onCycle(byID map[string]*driver.MetricDataQuery, id string, path, done map[string]bool) bool {
	if path[id] {
		return true
	}

	q, ok := byID[id]
	if !ok || done[id] {
		return false
	}

	path[id] = true
	refs, _ := References(q.Expression)

	for _, ref := range refs {
		if onCycle(byID, ref, path, done) {
			return true
		}
	}

	delete(path, id)

	done[id] = true

	return false
}
