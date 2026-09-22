package cloudwatch

import (
	"context"
	"sort"
	"strings"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// The cores in this file hold the ListMetrics and GetMetricStatistics logic.
// Both the query and the CBOR codecs call them, so the two protocols cannot
// drift apart again.

// listMetricsPageSize is the number of metrics AWS returns per ListMetrics page.
const listMetricsPageSize = 500

type dimensionFilterCBR struct {
	Name  string `cbor:"Name"`
	Value string `cbor:"Value,omitempty"`
}

// listMetricsInput is the shared ListMetrics request. The CBOR codec decodes
// straight into it and the query codec fills it from the form.
type listMetricsInput struct {
	Namespace  string               `cbor:"Namespace,omitempty"`
	MetricName string               `cbor:"MetricName,omitempty"`
	Dimensions []dimensionFilterCBR `cbor:"Dimensions,omitempty"`
	NextToken  string               `cbor:"NextToken,omitempty"`
}

type listMetricsResult struct {
	Metrics   []mondriver.MetricIdentifier
	NextToken string
}

func (h *Handler) listMetricsCore(ctx context.Context, in listMetricsInput) (listMetricsResult, error) {
	var rows []mondriver.MetricIdentifier

	// An exact AWS/IPAM request returns only the synthetic IPAM metrics.
	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		rows = h.ipamMetricRows(ctx)
	} else {
		all, err := h.allMetricRows(ctx)
		if err != nil {
			return listMetricsResult{}, err
		}

		rows = all

		if h.ipam != nil && in.Namespace == "" {
			rows = append(rows, h.ipamMetricRows(ctx)...)
		}
	}

	matched := filterMetricRows(rows, in)
	sort.SliceStable(matched, func(i, j int) bool {
		return metricRowKey(matched[i]) < metricRowKey(matched[j])
	})

	from, to, next := pageWindow(len(matched), decodeOffsetToken(in.NextToken), listMetricsPageSize)

	res := listMetricsResult{Metrics: matched[from:to]}
	if next > 0 {
		res.NextToken = encodeOffsetToken(next)
	}

	return res, nil
}

// metricRowKey gives a stable sort key so pages do not shift between calls.
func metricRowKey(m mondriver.MetricIdentifier) string {
	parts := make([]string, 0, len(m.Dimensions))
	for k, v := range m.Dimensions {
		parts = append(parts, k+"="+v)
	}

	sort.Strings(parts)

	return m.Namespace + "\x00" + m.MetricName + "\x00" + strings.Join(parts, ",")
}

func filterMetricRows(rows []mondriver.MetricIdentifier, in listMetricsInput) []mondriver.MetricIdentifier {
	out := make([]mondriver.MetricIdentifier, 0, len(rows))

	for _, row := range rows {
		if in.Namespace != "" && row.Namespace != in.Namespace {
			continue
		}

		if in.MetricName != "" && row.MetricName != in.MetricName {
			continue
		}

		if !rowMatchesDimensionFilters(row.Dimensions, in.Dimensions) {
			continue
		}

		out = append(out, row)
	}

	return out
}

// rowMatchesDimensionFilters follows the AWS DimensionFilter rules. A filter
// with a Value needs an exact match. A filter with only a Name needs that
// dimension to exist. Extra dimensions on the metric are fine.
func rowMatchesDimensionFilters(have map[string]string, filters []dimensionFilterCBR) bool {
	for _, f := range filters {
		v, ok := have[f.Name]
		if !ok {
			return false
		}

		if f.Value != "" && v != f.Value {
			return false
		}
	}

	return true
}

// detailedMetricLister is the AWS-local capability that enumerates every metric
// with its namespace, backing a namespace-less ListMetrics. The shared
// Monitoring interface only lists names within a single namespace.
type detailedMetricLister interface {
	ListMetricsDetailed(ctx context.Context) ([]mondriver.MetricIdentifier, error)
}

// allMetricRows falls back to the names-only driver list when the provider
// cannot enumerate dimensions.
func (h *Handler) allMetricRows(ctx context.Context) ([]mondriver.MetricIdentifier, error) {
	if dl, ok := h.monitoring.(detailedMetricLister); ok {
		return dl.ListMetricsDetailed(ctx)
	}

	names, err := h.monitoring.ListMetrics(ctx, "")
	if err != nil {
		return nil, err
	}

	out := make([]mondriver.MetricIdentifier, 0, len(names))
	for _, name := range names {
		out = append(out, mondriver.MetricIdentifier{MetricName: name})
	}

	return out, nil
}

func (h *Handler) ipamMetricRows(ctx context.Context) []mondriver.MetricIdentifier {
	metrics := h.ipam.IpamMetrics(ctx)
	out := make([]mondriver.MetricIdentifier, 0, len(metrics))

	for _, mtr := range metrics {
		out = append(out, mondriver.MetricIdentifier{
			Namespace:  netdriver.IpamMetricNamespace,
			MetricName: mtr.MetricName,
			Dimensions: mtr.Dimensions,
		})
	}

	return out
}

// getMetricStatisticsInput is the shared GetMetricStatistics request. The
// CBOR codec decodes straight into it and the query codec fills it from the form.
type getMetricStatisticsInput struct {
	Namespace  string         `cbor:"Namespace"`
	MetricName string         `cbor:"MetricName"`
	StartTime  *time.Time     `cbor:"StartTime,omitempty"`
	EndTime    *time.Time     `cbor:"EndTime,omitempty"`
	Period     int            `cbor:"Period"`
	Statistics []string       `cbor:"Statistics,omitempty"`
	Dimensions []dimensionCBR `cbor:"Dimensions,omitempty"`
}

// datapoint is one GetMetricStatistics datapoint. A nil statistic was not
// requested and must be left off the wire. A requested 0 must still be sent.
type datapoint struct {
	Timestamp   time.Time
	SampleCount *float64
	Average     *float64
	Sum         *float64
	Minimum     *float64
	Maximum     *float64
	Unit        string
}

type getMetricStatisticsResult struct {
	Label      string
	Datapoints []datapoint
}

func (h *Handler) getMetricStatisticsCore(
	ctx context.Context, in *getMetricStatisticsInput,
) (getMetricStatisticsResult, error) {
	// Callers often ask for several statistics at once and expect all of them
	// on each datapoint. Average is used only when none was requested.
	stats := in.Statistics
	if len(stats) == 0 {
		stats = []string{statAverage}
	}

	dims := toDimensionMap(in.Dimensions)

	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		return h.ipamMetricStatistics(ctx, in.MetricName, dims, stats), nil
	}

	var start, end time.Time
	if in.StartTime != nil {
		start = *in.StartTime
	}

	if in.EndTime != nil {
		end = *in.EndTime
	}

	acc := newDatapointAcc()

	for _, stat := range stats {
		res, err := h.monitoring.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  in.Namespace,
			MetricName: in.MetricName,
			Dimensions: dims,
			StartTime:  start,
			EndTime:    end,
			Period:     in.Period,
			Stat:       stat,
		})
		if err != nil {
			return getMetricStatisticsResult{}, err
		}

		acc.add(res, stat)
	}

	return getMetricStatisticsResult{Label: in.MetricName, Datapoints: acc.datapoints()}, nil
}

// ipamMetricStatistics returns one datapoint for a derived AWS/IPAM metric.
// IPAM metrics are point-in-time values, so every statistic equals the value.
func (h *Handler) ipamMetricStatistics(
	ctx context.Context, name string, dims map[string]string, stats []string,
) getMetricStatisticsResult {
	for _, mtr := range h.ipam.IpamMetrics(ctx) {
		if mtr.MetricName != name || !dimensionsMatch(mtr.Dimensions, dims) {
			continue
		}

		dp := datapoint{Timestamp: time.Unix(0, 0).UTC(), Unit: mtr.Unit}
		for _, stat := range stats {
			setStat(&dp, stat, mtr.Value)
		}

		return getMetricStatisticsResult{Label: name, Datapoints: []datapoint{dp}}
	}

	return getMetricStatisticsResult{Label: name}
}

// dimensionsMatch reports whether every requested dimension is present in have.
func dimensionsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}

	return true
}

// datapointAcc merges one result per statistic into one datapoint per
// timestamp, so each datapoint carries every requested statistic.
type datapointAcc struct {
	byTS  map[int64]*datapoint
	order []int64
	unit  string
}

func newDatapointAcc() *datapointAcc {
	return &datapointAcc{byTS: map[int64]*datapoint{}}
}

func (a *datapointAcc) add(res *mondriver.MetricDataResult, stat string) {
	if res == nil {
		return
	}

	if a.unit == "" {
		a.unit = res.Unit
	}

	for i := range res.Timestamps {
		ts := res.Timestamps[i].UTC()
		key := ts.UnixNano()

		dp, ok := a.byTS[key]
		if !ok {
			dp = &datapoint{Timestamp: ts}
			a.byTS[key] = dp
			a.order = append(a.order, key)
		}

		setStat(dp, stat, res.Values[i])
	}
}

// datapoints returns the merged datapoints oldest first.
func (a *datapointAcc) datapoints() []datapoint {
	unit := a.unit
	if unit == "" {
		unit = defaultMetricUnit
	}

	sort.Slice(a.order, func(i, j int) bool { return a.order[i] < a.order[j] })

	out := make([]datapoint, 0, len(a.order))

	for _, key := range a.order {
		dp := a.byTS[key]
		dp.Unit = unit
		out = append(out, *dp)
	}

	return out
}

func setStat(dp *datapoint, stat string, value float64) {
	v := value

	switch stat {
	case statSum:
		dp.Sum = &v
	case statMinimum:
		dp.Minimum = &v
	case statMaximum:
		dp.Maximum = &v
	case statSampleCount:
		dp.SampleCount = &v
	default:
		dp.Average = &v
	}
}
