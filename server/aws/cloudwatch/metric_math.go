package cloudwatch

// This file implements the metric-math evaluation GetMetricData performs when a
// query carries an Expression instead of a MetricStat. Expressions reference the
// Ids of other queries and combine their series with arithmetic operators
// (+ - * / and parentheses), evaluated element-wise, matching how CloudWatch
// computes a metric-math row (e.g. "m1*2" or "m1/m2*100").

import (
	"context"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// mathSeries is a resolved time series: aligned timestamps and values.
type mathSeries struct {
	timestamps []time.Time
	values     []float64
}

// mathEvaluator resolves each GetMetricData query Id to its series, fetching
// MetricStat queries from the monitoring driver and computing Expression queries
// from the series they reference. Results are memoized so a shared input query
// is fetched once, and an in-progress guard breaks any reference cycle.
type mathEvaluator struct {
	ctx        context.Context //nolint:containedctx // short-lived per-request evaluator
	monitoring mondriver.Monitoring
	byID       map[string]metricDataQueryCBR
	start, end time.Time
	memo       map[string]mathSeries
	inProgress map[string]bool
}

func newMathEvaluator(
	ctx context.Context, mon mondriver.Monitoring, queries []metricDataQueryCBR, start, end time.Time,
) *mathEvaluator {
	byID := make(map[string]metricDataQueryCBR, len(queries))
	for i := range queries {
		byID[queries[i].ID] = queries[i]
	}

	return &mathEvaluator{
		ctx:        ctx,
		monitoring: mon,
		byID:       byID,
		start:      start,
		end:        end,
		memo:       make(map[string]mathSeries, len(queries)),
		inProgress: make(map[string]bool, len(queries)),
	}
}

// resolve returns the series for a query Id. A driver failure while fetching a
// MetricStat query is propagated; an unknown Id or an unparsable expression
// resolves to an empty series rather than an error.
func (e *mathEvaluator) resolve(id string) (mathSeries, error) {
	if s, ok := e.memo[id]; ok {
		return s, nil
	}

	if e.inProgress[id] {
		return mathSeries{}, nil // reference cycle: stop recursing
	}

	q, ok := e.byID[id]
	if !ok {
		return mathSeries{}, nil
	}

	e.inProgress[id] = true
	defer delete(e.inProgress, id)

	series, err := e.resolveQuery(q)
	if err != nil {
		return mathSeries{}, err
	}

	e.memo[id] = series

	return series, nil
}

func (e *mathEvaluator) resolveQuery(q metricDataQueryCBR) (mathSeries, error) {
	switch {
	case q.MetricStat != nil:
		return e.resolveMetricStat(q.MetricStat)
	case q.Expression != "":
		return e.evalExpression(q.Expression)
	default:
		return mathSeries{}, nil
	}
}

func (e *mathEvaluator) resolveMetricStat(ms *metricStatCBR) (mathSeries, error) {
	res, err := e.monitoring.GetMetricData(e.ctx, mondriver.GetMetricInput{
		Namespace:  ms.Metric.Namespace,
		MetricName: ms.Metric.MetricName,
		Dimensions: toDimensionMap(ms.Metric.Dimensions),
		StartTime:  e.start,
		EndTime:    e.end,
		Period:     ms.Period,
		Stat:       ms.Stat,
	})
	if err != nil {
		return mathSeries{}, err
	}

	if res == nil {
		return mathSeries{}, nil
	}

	return mathSeries{timestamps: res.Timestamps, values: res.Values}, nil
}

// evalExpression parses and evaluates a metric-math expression, resolving each
// referenced query Id to its series. A parse error yields an empty series.
func (e *mathEvaluator) evalExpression(expr string) (mathSeries, error) {
	p := &mathParser{tokens: tokenizeMath(expr)}

	val, err := p.parse()
	if err != nil || !p.atEnd() {
		return mathSeries{}, nil
	}

	resolved, driverErr := val.evaluate(e)
	if driverErr != nil {
		return mathSeries{}, driverErr
	}

	return resolved.asSeries(), nil
}
