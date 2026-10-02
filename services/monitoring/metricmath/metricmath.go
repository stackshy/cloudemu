// Package metricmath evaluates CloudWatch metric-math query lists. A list
// holds MetricStat entries, which read one metric, and Expression entries,
// which combine other entries by ID with + - * / and parentheses. GetMetricData
// and metric-math alarms both use it, so the wire layer and the provider
// compute the same series.
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
	memo       map[memoKey]Series
	inProgress map[string]bool
	band       BandConfig
	history    *Evaluator
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
		memo:       make(map[memoKey]Series, len(queries)),
		inProgress: make(map[string]bool, len(queries)),
	}
}

// Resolve returns the series of the entry with this ID. A fetch error is
// returned. An unknown ID or an expression outside the supported syntax
// gives an empty series.
func (e *Evaluator) Resolve(id string) (Series, error) {
	return e.resolve(id, 0)
}

// ResolveAt is Resolve with every metric read at period seconds. An alarm
// uses it so each point fills exactly one of its evaluation periods.
func (e *Evaluator) ResolveAt(id string, period int) (Series, error) {
	return e.resolve(id, period)
}

// resolve reads the entry at period. Zero means the entry's own period.
func (e *Evaluator) resolve(id string, period int) (Series, error) {
	key := memoKey{id: id, period: period}
	if s, ok := e.memo[key]; ok {
		return s, nil
	}

	q, ok := e.byID[id]
	if !ok || e.inProgress[id] {
		return Series{}, nil
	}

	e.inProgress[id] = true
	defer delete(e.inProgress, id)

	s, err := e.resolveQuery(q, period)
	if err != nil {
		return Series{}, err
	}

	e.memo[key] = s

	return s, nil
}

func (e *Evaluator) resolveQuery(q *driver.MetricDataQuery, period int) (Series, error) {
	switch {
	case q.MetricStat != nil:
		if period == 0 {
			period = q.MetricStat.Period
		}

		return e.fetch(q.MetricStat, period)
	case q.Expression != "":
		// An expression's own Period sets the granularity of everything it
		// reads. Without one it passes on the period it was asked for.
		if q.Period > 0 {
			period = q.Period
		}

		return e.evalExpression(q.Expression, period)
	default:
		return Series{}, nil
	}
}

func (e *Evaluator) evalExpression(expr string, period int) (Series, error) {
	n, ok := parseExpression(expr)
	if !ok {
		return Series{}, nil
	}

	res, err := n.evaluate(scope{e: e, period: period})
	if err != nil {
		return Series{}, err
	}

	return res.asSeries(), nil
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
