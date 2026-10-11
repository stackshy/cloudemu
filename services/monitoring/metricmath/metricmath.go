// Package metricmath evaluates CloudWatch metric-math query lists. A list
// holds MetricStat entries, which read one metric, and Expression entries,
// which combine other entries by ID with + - * /, parentheses, comparison and
// logical operators, and the IF, FILL and SEARCH functions. GetMetricData and
// metric-math alarms both use it, so the wire layer and the provider compute
// the same series.
//
// ANOMALY_DETECTION_BAND(id, k) is supported as a whole expression. It is an
// approximation of the AWS model: mean -/+ k standard deviations of the
// previous two weeks of the input. See band.go.
package metricmath

import (
	"maps"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Series is a resolved time series with one value per timestamp.
type Series struct {
	Timestamps []time.Time
	Values     []float64
}

// Fetcher returns the series of one metric aggregated over period seconds.
type Fetcher func(ms *driver.MetricStat, period int) (Series, error)

// memoKey caches a resolved entry per period. An entry can be read at its own
// period and at the period of an expression that references it.
type memoKey struct {
	id     string
	period int
}

// Evaluator resolves the entries of one query list. A shared input is
// fetched once per period, and a reference cycle resolves to an empty series.
type Evaluator struct {
	queries    []driver.MetricDataQuery
	byID       map[string]*driver.MetricDataQuery
	fetch      Fetcher
	memo       map[memoKey]result
	inProgress map[string]bool
	band       BandConfig
	history    *Evaluator
	start, end time.Time
	lister     Lister
}

// New returns an evaluator over queries that reads metrics through fetch.
func New(queries []driver.MetricDataQuery, fetch Fetcher) *Evaluator {
	byID := make(map[string]*driver.MetricDataQuery, len(queries))
	for i := range queries {
		byID[queries[i].ID] = &queries[i]
	}

	return &Evaluator{
		queries:    queries,
		byID:       byID,
		fetch:      fetch,
		memo:       make(map[memoKey]result, len(queries)),
		inProgress: make(map[string]bool, len(queries)),
	}
}

// WithRange sets the time range the metrics are read over, [start, end).
// FILL fills the periods of this range that have no value, so without it
// FILL leaves its input as is. It returns e.
func (e *Evaluator) WithRange(start, end time.Time) *Evaluator {
	e.start, e.end = start, end

	return e
}

// WithSearch sets where SEARCH finds metrics. Without it a SEARCH finds
// nothing. It returns e.
func (e *Evaluator) WithSearch(l Lister) *Evaluator {
	e.lister = l

	return e
}

// Resolve returns the series of the entry with this ID. A fetch error is
// returned. An unknown ID or an expression outside the supported syntax
// gives an empty series, and so does an expression that returns several
// series, such as a SEARCH that finds more than one metric.
func (e *Evaluator) Resolve(id string) (Series, error) {
	r, err := e.resolve(id, 0)
	if err != nil {
		return Series{}, err
	}

	return r.asSeries(), nil
}

// ResolveAll returns every series of the entry with this ID: one for a
// metric or a single-series expression, and one per found metric for an
// expression that returns an array of series, such as a SEARCH.
func (e *Evaluator) ResolveAll(id string) ([]Labeled, error) {
	r, err := e.resolve(id, 0)
	if err != nil {
		return nil, err
	}

	return r.labeled(), nil
}

// ResolveAt is Resolve with every metric read at period seconds. An alarm
// uses it so each point fills exactly one of its evaluation periods.
func (e *Evaluator) ResolveAt(id string, period int) (Series, error) {
	r, err := e.resolve(id, period)
	if err != nil {
		return Series{}, err
	}

	return r.asSeries(), nil
}

// resolve reads the entry at period. Zero means the entry's own period.
func (e *Evaluator) resolve(id string, period int) (result, error) {
	key := memoKey{id: id, period: period}
	if r, ok := e.memo[key]; ok {
		return r, nil
	}

	q, ok := e.byID[id]
	if !ok || e.inProgress[id] {
		return result{}, nil
	}

	e.inProgress[id] = true
	defer delete(e.inProgress, id)

	r, err := e.resolveQuery(q, period)
	if err != nil {
		return result{}, err
	}

	e.memo[key] = r

	return r, nil
}

func (e *Evaluator) resolveQuery(q *driver.MetricDataQuery, period int) (result, error) {
	switch {
	case q.MetricStat != nil:
		if period == 0 {
			period = q.MetricStat.Period
		}

		s, err := e.fetch(q.MetricStat, period)

		return result{series: s}, err
	case q.Expression != "":
		// An expression's own Period sets the granularity of everything it
		// reads. Without one it passes on the period it was asked for.
		if q.Period > 0 {
			period = q.Period
		}

		return e.evalExpression(q, period)
	default:
		return result{}, nil
	}
}

func (e *Evaluator) evalExpression(q *driver.MetricDataQuery, period int) (result, error) {
	n, ok := parseExpression(q.Expression)
	if !ok {
		return result{}, nil
	}

	// The expression's points are period seconds apart, or, when the
	// entries keep their own periods, as far apart as the points it reads.
	grid := period
	if grid == 0 {
		grid = Period(e.queries, q)
	}

	res, err := n.evaluate(scope{e: e, period: period, gridPeriod: grid})
	if err != nil {
		return result{}, err
	}

	if !res.isArray {
		res = result{series: res.asSeries()}
	}

	return res, nil
}

// References returns the IDs an expression reads. ok is false when the
// expression is outside the supported syntax, for example a function call.
func References(expr string) (ids []string, ok bool) {
	n, ok := parseExpression(expr)
	if !ok {
		return nil, false
	}

	collectRefs(n, &ids)

	return ids, true
}

// ReturnsData reports whether an entry is returned. Nil ReturnData means true.
func ReturnsData(q *driver.MetricDataQuery) bool {
	return q.ReturnData == nil || *q.ReturnData
}

// Watched returns the entries that return data, leaving out the entry named
// by thresholdID. A valid alarm has exactly one.
func Watched(queries []driver.MetricDataQuery, thresholdID string) []*driver.MetricDataQuery {
	var out []*driver.MetricDataQuery

	for i := range queries {
		if queries[i].ID != thresholdID && ReturnsData(&queries[i]) {
			out = append(out, &queries[i])
		}
	}

	return out
}

// Clone returns a deep copy of a query list, so a stored alarm never shares
// pointers or maps with its caller.
func Clone(queries []driver.MetricDataQuery) []driver.MetricDataQuery {
	if len(queries) == 0 {
		return nil
	}

	out := make([]driver.MetricDataQuery, len(queries))

	for i := range queries {
		out[i] = queries[i]

		if rd := queries[i].ReturnData; rd != nil {
			v := *rd
			out[i].ReturnData = &v
		}

		if ms := queries[i].MetricStat; ms != nil {
			c := *ms
			c.Dimensions = maps.Clone(ms.Dimensions)
			out[i].MetricStat = &c
		}
	}

	return out
}
