package cloudwatch

import (
	"context"
	"sort"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// The cores in this file hold the GetMetricData and DescribeAlarmsForMetric
// logic. The query and the CBOR codecs both call them.

// defaultMaxDatapoints is the GetMetricData datapoint budget per page when
// the caller omits MaxDatapoints.
const defaultMaxDatapoints = 100800

const (
	scanByDescending      = "TimestampDescending"
	statusCodePartialData = "PartialData"
)

// getMetricDataInput is the shared GetMetricData request. The CBOR codec
// decodes straight into it and the query codec fills it from the form.
type getMetricDataInput struct {
	MetricDataQueries []metricDataQueryCBR `cbor:"MetricDataQueries"`
	StartTime         *time.Time           `cbor:"StartTime,omitempty"`
	EndTime           *time.Time           `cbor:"EndTime,omitempty"`
	MaxDatapoints     int                  `cbor:"MaxDatapoints,omitempty"`
	NextToken         string               `cbor:"NextToken,omitempty"`
	ScanBy            string               `cbor:"ScanBy,omitempty"`
}

// metricDataRow is one GetMetricData result row, with points in ScanBy order.
type metricDataRow struct {
	ID         string
	Label      string
	StatusCode string
	Timestamps []time.Time
	Values     []float64
}

type getMetricDataResult struct {
	Rows      []metricDataRow
	NextToken string
}

func (h *Handler) getMetricDataCore(ctx context.Context, in *getMetricDataInput) (getMetricDataResult, error) {
	descending, err := scanByIsDescending(in.ScanBy)
	if err != nil {
		return getMetricDataResult{}, err
	}

	offset, err := offsetFromToken(in.NextToken, errInvalidNextToken)
	if err != nil {
		return getMetricDataResult{}, err
	}

	queries := toDriverQueries(in.MetricDataQueries)
	eval := h.metricDataEvaluator(ctx, queries, timeOrZero(in.StartTime), timeOrZero(in.EndTime))
	rows := make([]metricDataRow, 0, len(in.MetricDataQueries))

	for i := range in.MetricDataQueries {
		q := &in.MetricDataQueries[i]

		// ReturnData=false rows only feed other queries and are not returned.
		if !metricmath.ReturnsData(&queries[i]) {
			continue
		}

		qrows, err := metricDataRows(eval, q, descending)
		if err != nil {
			return getMetricDataResult{}, err
		}

		rows = append(rows, qrows...)
	}

	page, next := pageMetricData(rows, offset, in.MaxDatapoints, descending)

	return getMetricDataResult{Rows: page, NextToken: next}, nil
}

// bandExclusionSource is the AWS-local capability that returns the excluded
// training ranges of the detector behind a band.
type bandExclusionSource interface {
	BandExclusions(queries []mondriver.MetricDataQuery, inputID string) []mondriver.TimeRange
}

// metricDataEvaluator builds the evaluator for one GetMetricData call. A band
// trains on the two weeks before start as well.
func (h *Handler) metricDataEvaluator(
	ctx context.Context, queries []mondriver.MetricDataQuery, start, end time.Time,
) *metricmath.Evaluator {
	cfg := metricmath.BandConfig{History: h.metricFetcher(ctx, start.Add(-metricmath.TrainingWindow), end)}

	if src, ok := h.monitoring.(bandExclusionSource); ok {
		cfg.Excluded = src.BandExclusions
	}

	return metricmath.New(queries, h.metricFetcher(ctx, start, end)).WithBand(cfg)
}

// metricDataRows returns the result rows of one query. A band returns two
// rows with the query's Id, the lower edge and then the upper edge.
func metricDataRows(eval *metricmath.Evaluator, q *metricDataQueryCBR, descending bool) ([]metricDataRow, error) {
	band, isBand, err := eval.Band(q.ID)
	if err != nil {
		return nil, err
	}

	if isBand {
		lower := metricmath.Series{Timestamps: band.Timestamps, Values: band.Lower}
		upper := metricmath.Series{Timestamps: band.Timestamps, Values: band.Upper}

		return []metricDataRow{buildMetricDataRow(q, lower, descending), buildMetricDataRow(q, upper, descending)}, nil
	}

	series, err := eval.Resolve(q.ID)
	if err != nil {
		return nil, err
	}

	return []metricDataRow{buildMetricDataRow(q, series, descending)}, nil
}

// metricFetcher reads one metric from the monitoring driver over [start, end).
func (h *Handler) metricFetcher(ctx context.Context, start, end time.Time) metricmath.Fetcher {
	return func(ms *mondriver.MetricStat, period int) (metricmath.Series, error) {
		res, err := h.monitoring.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  ms.Namespace,
			MetricName: ms.MetricName,
			Dimensions: ms.Dimensions,
			StartTime:  start,
			EndTime:    end,
			Period:     period,
			Stat:       ms.Stat,
			Unit:       ms.Unit,
		})
		if err != nil || res == nil {
			return metricmath.Series{}, err
		}

		return metricmath.Series{Timestamps: res.Timestamps, Values: res.Values}, nil
	}
}

// scanByIsDescending validates ScanBy. An empty value means
// TimestampDescending, which is the AWS default.
func scanByIsDescending(scanBy string) (bool, error) {
	switch scanBy {
	case "", scanByDescending:
		return true, nil
	case scanByAscending:
		return false, nil
	default:
		return false, newWireError(errValidation, "1 validation error detected: Value '"+scanBy+
			"' at 'scanBy' failed to satisfy constraint: Member must satisfy enum value set: [TimestampDescending, TimestampAscending]")
	}
}

// buildMetricDataRow copies the series in ScanBy order. It copies so the
// evaluator's cached series, which other queries reuse, stay untouched.
func buildMetricDataRow(q *metricDataQueryCBR, series metricmath.Series, descending bool) metricDataRow {
	n := len(series.Timestamps)

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}

	sort.SliceStable(order, func(i, j int) bool {
		a, b := series.Timestamps[order[i]], series.Timestamps[order[j]]
		if descending {
			return a.After(b)
		}

		return a.Before(b)
	})

	row := metricDataRow{
		ID: q.ID, Label: metricDataLabel(q), StatusCode: statusCodeComplete,
		Timestamps: make([]time.Time, n), Values: make([]float64, n),
	}

	for i, j := range order {
		row.Timestamps[i] = series.Timestamps[j].UTC()
		row.Values[i] = series.Values[j]
	}

	return row
}

// metricDataLabel resolves the response label for a query: the caller-supplied
// Label, else the metric name for a MetricStat query, else the query Id.
func metricDataLabel(q *metricDataQueryCBR) string {
	if q.Label != "" {
		return q.Label
	}

	if q.MetricStat != nil {
		return q.MetricStat.Metric.MetricName
	}

	return q.ID
}

// pointRef locates one datapoint inside the result rows.
type pointRef struct {
	row, idx int
	ts       time.Time
}

// pageMetricData pages by datapoints in time order across all rows, like
// AWS: a descending scan puts the newest points of every row on page 1. The
// token is the offset into that time-ordered list. A row cut off by the page
// end gets StatusCode PartialData. Rows with no points appear on page 1 only.
func pageMetricData(rows []metricDataRow, offset, budget int, descending bool) (page []metricDataRow, next string) {
	if budget <= 0 {
		budget = defaultMaxDatapoints
	}

	refs := timeOrderedPoints(rows, descending)
	from := min(offset, len(refs))
	to := min(from+budget, len(refs))

	picked := make([][]int, len(rows))
	for _, ref := range refs[from:to] {
		picked[ref.row] = append(picked[ref.row], ref.idx)
	}

	seen := make([]int, len(rows))
	for _, ref := range refs[:to] {
		seen[ref.row]++
	}

	page = make([]metricDataRow, 0, len(rows))

	for r := range rows {
		src := &rows[r]
		if len(src.Timestamps) == 0 {
			if from == 0 {
				page = append(page, *src)
			}

			continue
		}

		if len(picked[r]) == 0 {
			continue
		}

		page = append(page, pagedRow(src, picked[r], seen[r] < len(src.Timestamps)))
	}

	if to < len(refs) {
		next = encodeOffsetToken(to)
	}

	return page, next
}

// timeOrderedPoints lists every point of every row in ScanBy time order. The
// sort is stable, so ties keep query order and each row keeps its own order.
func timeOrderedPoints(rows []metricDataRow, descending bool) []pointRef {
	var refs []pointRef

	for r := range rows {
		for i, ts := range rows[r].Timestamps {
			refs = append(refs, pointRef{row: r, idx: i, ts: ts})
		}
	}

	sort.SliceStable(refs, func(i, j int) bool {
		if descending {
			return refs[i].ts.After(refs[j].ts)
		}

		return refs[i].ts.Before(refs[j].ts)
	})

	return refs
}

func pagedRow(src *metricDataRow, idx []int, partial bool) metricDataRow {
	row := metricDataRow{
		ID: src.ID, Label: src.Label, StatusCode: statusCodeComplete,
		Timestamps: make([]time.Time, 0, len(idx)), Values: make([]float64, 0, len(idx)),
	}

	if partial {
		row.StatusCode = statusCodePartialData
	}

	for _, i := range idx {
		row.Timestamps = append(row.Timestamps, src.Timestamps[i])
		row.Values = append(row.Values, src.Values[i])
	}

	return row
}

// describeAlarmsForMetricInput is the shared DescribeAlarmsForMetric request.
type describeAlarmsForMetricInput struct {
	Namespace         string         `cbor:"Namespace"`
	MetricName        string         `cbor:"MetricName"`
	Dimensions        []dimensionCBR `cbor:"Dimensions,omitempty"`
	Statistic         string         `cbor:"Statistic,omitempty"`
	ExtendedStatistic string         `cbor:"ExtendedStatistic,omitempty"`
	Period            int            `cbor:"Period,omitempty"`
	Unit              string         `cbor:"Unit,omitempty"`
}

// describeAlarmsForMetricCore returns the metric alarms on exactly this
// metric, sorted by name. Dimensions must match the full set.
func (h *Handler) describeAlarmsForMetricCore(
	ctx context.Context, in *describeAlarmsForMetricInput,
) ([]mondriver.AlarmInfo, error) {
	if in.Namespace == "" {
		return nil, newWireError(errMissingParameter, "The parameter Namespace is required.")
	}

	if in.MetricName == "" {
		return nil, newWireError(errMissingParameter, "The parameter MetricName is required.")
	}

	alarms, err := h.monitoring.DescribeAlarms(ctx, nil)
	if err != nil {
		return nil, err
	}

	wantDims := toDimensionMap(in.Dimensions)
	out := make([]mondriver.AlarmInfo, 0, len(alarms))

	for i := range alarms {
		if alarmMatchesMetric(&alarms[i], in, wantDims) {
			out = append(out, alarms[i])
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// alarmMatchesMetric applies the DescribeAlarmsForMetric filters. An empty
// filter matches any value.
func alarmMatchesMetric(a *mondriver.AlarmInfo, in *describeAlarmsForMetricInput, wantDims map[string]string) bool {
	if a.Namespace != in.Namespace || a.MetricName != in.MetricName {
		return false
	}

	if in.Period != 0 && a.Period != in.Period {
		return false
	}

	return optionalMatch(in.Statistic, a.Statistic) &&
		optionalMatch(in.ExtendedStatistic, a.ExtendedStatistic) &&
		optionalMatch(in.Unit, a.Unit) &&
		dimensionsEqual(a.Dimensions, wantDims)
}

// optionalMatch reports whether have matches an optional filter.
func optionalMatch(want, have string) bool {
	return want == "" || want == have
}

func dimensionsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}
