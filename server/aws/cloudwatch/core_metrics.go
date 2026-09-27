package cloudwatch

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
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
	Namespace      string               `cbor:"Namespace,omitempty"`
	MetricName     string               `cbor:"MetricName,omitempty"`
	Dimensions     []dimensionFilterCBR `cbor:"Dimensions,omitempty"`
	NextToken      string               `cbor:"NextToken,omitempty"`
	RecentlyActive string               `cbor:"RecentlyActive,omitempty"`
}

// ListMetrics visibility windows from API_ListMetrics. A metric that has not
// had data for two weeks is not listed. RecentlyActive=PT3H, its only valid
// value, narrows that to three hours.
const (
	recentlyActivePT3H = "PT3H"
	recentlyActiveSpan = 3 * time.Hour
	listMetricsSpan    = 14 * 24 * time.Hour
)

// listMetricsWindow is how far back a series must have had data to be
// listed.
func listMetricsWindow(recentlyActive string) (time.Duration, error) {
	switch recentlyActive {
	case "":
		return listMetricsSpan, nil
	case recentlyActivePT3H:
		return recentlyActiveSpan, nil
	default:
		return 0, newWireError(errInvalidParameterValue, "The parameter RecentlyActive must be a value in the set [PT3H].")
	}
}

type listMetricsResult struct {
	Metrics   []mondriver.MetricIdentifier
	NextToken string
}

func (h *Handler) listMetricsCore(ctx context.Context, in listMetricsInput) (listMetricsResult, error) {
	// ListMetrics documents only InvalidParameterValue, not InvalidNextToken.
	offset, err := offsetFromToken(in.NextToken, errInvalidParameterValue)
	if err != nil {
		return listMetricsResult{}, err
	}

	within, err := listMetricsWindow(in.RecentlyActive)
	if err != nil {
		return listMetricsResult{}, err
	}

	var rows []mondriver.MetricIdentifier

	// An exact AWS/IPAM request returns only the synthetic IPAM metrics.
	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		rows = h.ipamMetricRows(ctx)
	} else {
		all, err := h.allMetricRows(ctx, within)
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

	from, to, next := pageWindow(len(matched), offset, listMetricsPageSize)

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

// activeMetricLister is the AWS-local capability that lists only the metrics
// that had data put within a recent span.
type activeMetricLister interface {
	ListMetricsActive(ctx context.Context, within time.Duration) ([]mondriver.MetricIdentifier, error)
}

// allMetricRows lists the metrics that had data within the span. It falls
// back to every metric when the provider keeps no receipt times, and to the
// names-only driver list when it cannot enumerate dimensions.
func (h *Handler) allMetricRows(ctx context.Context, within time.Duration) ([]mondriver.MetricIdentifier, error) {
	if al, ok := h.monitoring.(activeMetricLister); ok {
		return al.ListMetricsActive(ctx, within)
	}

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
	Unit       string         `cbor:"Unit,omitempty"`

	ExtendedStatistics []string `cbor:"ExtendedStatistics,omitempty"`
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
	// ExtendedStatistics maps each requested percentile to its value.
	ExtendedStatistics map[string]float64
}

type getMetricStatisticsResult struct {
	Label      string
	Datapoints []datapoint
}

// metricUnitLister is the AWS-local capability that lists the units a
// metric's data was stored under.
type metricUnitLister interface {
	MetricUnits(ctx context.Context, in *mondriver.GetMetricInput) []string
}

// maxExtendedStatistics is the most ExtendedStatistics one request may ask for.
const maxExtendedStatistics = 10

// validateStatistics applies the GetMetricStatistics rule: "you must specify
// either Statistics or ExtendedStatistics, but not both". ExtendedStatistics
// holds percentiles from p0.0 to p100 only.
func validateStatistics(in *getMetricStatisticsInput) error {
	switch {
	case len(in.Statistics) > 0 && len(in.ExtendedStatistics) > 0:
		return newWireError(errInvalidParameterCombo,
			"Must specify either Statistics or ExtendedStatistics, but not both.")
	case len(in.Statistics) == 0 && len(in.ExtendedStatistics) == 0:
		return newWireError(errMissingParameter, "Must specify either Statistics or ExtendedStatistics.")
	case len(in.ExtendedStatistics) > maxExtendedStatistics:
		return newWireError(errInvalidParameterValue, "The collection ExtendedStatistics must not have more than "+
			strconv.Itoa(maxExtendedStatistics)+" members.")
	}

	for _, s := range in.ExtendedStatistics {
		if !alarmeval.IsPercentile(s) {
			return newWireError(errInvalidParameterValue, "The value "+s+
				" for parameter ExtendedStatistics is not supported. Specify a percentile between p0.0 and p100.")
		}
	}

	return nil
}

// requestedStat is one statistic a GetMetricStatistics request asks for.
type requestedStat struct {
	name     string
	extended bool
}

func requestedStats(in *getMetricStatisticsInput) []requestedStat {
	out := make([]requestedStat, 0, len(in.Statistics)+len(in.ExtendedStatistics))

	for _, s := range in.Statistics {
		out = append(out, requestedStat{name: s})
	}

	for _, s := range in.ExtendedStatistics {
		out = append(out, requestedStat{name: s, extended: true})
	}

	return out
}

func (h *Handler) getMetricStatisticsCore(
	ctx context.Context, in *getMetricStatisticsInput,
) (getMetricStatisticsResult, error) {
	if err := validateStatistics(in); err != nil {
		return getMetricStatisticsResult{}, err
	}

	// Callers often ask for several statistics at once and expect all of them
	// on each datapoint.
	stats := requestedStats(in)
	dims := toDimensionMap(in.Dimensions)

	if h.ipam != nil && in.Namespace == netdriver.IpamMetricNamespace {
		return h.ipamMetricStatistics(ctx, in.MetricName, dims, in.Unit, stats), nil
	}

	q := mondriver.GetMetricInput{
		Namespace:  in.Namespace,
		MetricName: in.MetricName,
		Dimensions: dims,
		StartTime:  timeOrZero(in.StartTime),
		EndTime:    timeOrZero(in.EndTime),
		Period:     in.Period,
		Unit:       in.Unit,
	}

	acc := newDatapointAcc()

	for _, unit := range h.statisticUnits(ctx, &q) {
		q.Unit = unit

		for _, stat := range stats {
			q.Stat = stat.name

			res, err := h.monitoring.GetMetricData(ctx, q)
			if err != nil {
				return getMetricStatisticsResult{}, err
			}

			acc.add(res, stat, unit)
		}
	}

	return getMetricStatisticsResult{Label: in.MetricName, Datapoints: acc.datapoints()}, nil
}

// statisticUnits returns the units to read one at a time. AWS keeps data put
// with different units apart, so an omitted Unit gives one datapoint per unit.
// A provider that cannot list its units is read once for any unit.
func (h *Handler) statisticUnits(ctx context.Context, q *mondriver.GetMetricInput) []string {
	if q.Unit != "" {
		return []string{q.Unit}
	}

	if l, ok := h.monitoring.(metricUnitLister); ok {
		return l.MetricUnits(ctx, q)
	}

	return []string{""}
}

// ipamMetricStatistics returns one datapoint for a derived AWS/IPAM metric.
// IPAM metrics are point-in-time values, so every statistic equals the value.
func (h *Handler) ipamMetricStatistics(
	ctx context.Context, name string, dims map[string]string, unit string, stats []requestedStat,
) getMetricStatisticsResult {
	for _, mtr := range h.ipam.IpamMetrics(ctx) {
		if mtr.MetricName != name || !dimensionsMatch(mtr.Dimensions, dims) || !alarmeval.MatchUnit(mtr.Unit, unit) {
			continue
		}

		dp := datapoint{Timestamp: time.Unix(0, 0).UTC(), Unit: alarmeval.EffectiveUnit(mtr.Unit)}
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

// datapointKey is one datapoint slot. Data with different units at the same
// timestamp are separate datapoints.
type datapointKey struct {
	ts   int64
	unit string
}

// datapointAcc merges one result per statistic into one datapoint per
// timestamp and unit, so each datapoint carries every requested statistic.
type datapointAcc struct {
	byKey map[datapointKey]*datapoint
	order []datapointKey
}

func newDatapointAcc() *datapointAcc {
	return &datapointAcc{byKey: map[datapointKey]*datapoint{}}
}

// add merges one statistic's result. unit is the unit that was asked for. When
// it is empty the result's own unit is used.
func (a *datapointAcc) add(res *mondriver.MetricDataResult, stat requestedStat, unit string) {
	if res == nil {
		return
	}

	if unit == "" {
		unit = alarmeval.EffectiveUnit(res.Unit)
	}

	for i := range res.Timestamps {
		ts := res.Timestamps[i].UTC()
		key := datapointKey{ts: ts.UnixNano(), unit: unit}

		dp, ok := a.byKey[key]
		if !ok {
			dp = &datapoint{Timestamp: ts, Unit: unit}
			a.byKey[key] = dp
			a.order = append(a.order, key)
		}

		setStat(dp, stat, res.Values[i])
	}
}

// datapoints returns the merged datapoints oldest first, then by unit.
func (a *datapointAcc) datapoints() []datapoint {
	sort.Slice(a.order, func(i, j int) bool {
		if a.order[i].ts != a.order[j].ts {
			return a.order[i].ts < a.order[j].ts
		}

		return a.order[i].unit < a.order[j].unit
	})

	out := make([]datapoint, 0, len(a.order))
	for _, key := range a.order {
		out = append(out, *a.byKey[key])
	}

	return out
}

func setStat(dp *datapoint, stat requestedStat, value float64) {
	v := value

	if stat.extended {
		if dp.ExtendedStatistics == nil {
			dp.ExtendedStatistics = map[string]float64{}
		}

		dp.ExtendedStatistics[stat.name] = v

		return
	}

	switch stat.name {
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

// putMetricDataCore validates every datum and then stores them. One bad
// datum rejects the whole request, so nothing is stored.
func (h *Handler) putMetricDataCore(ctx context.Context, in *putMetricDataInput) error {
	data := make([]mondriver.MetricDatum, 0, len(in.MetricData))

	for i := range in.MetricData {
		d := &in.MetricData[i]

		if d.Unit != "" && !alarmeval.ValidUnit(d.Unit) {
			return newWireError(errInvalidParameterValue, "The parameter MetricData.member."+strconv.Itoa(i+1)+
				".Unit must be a value in the set ["+strings.Join(alarmeval.Units(), ", ")+"]")
		}

		data = append(data, toMetricDatum(in.Namespace, d))
	}

	return h.monitoring.PutMetricData(ctx, data)
}

func toMetricDatum(namespace string, d *putMetricDatumCBR) mondriver.MetricDatum {
	// AWS stamps a datum without a timestamp with the time it was received.
	// The Go zero time would put it outside every query and alarm window.
	ts := time.Now().UTC()
	if d.Timestamp != nil {
		ts = *d.Timestamp
	}

	datum := mondriver.MetricDatum{
		Namespace:  namespace,
		MetricName: d.MetricName,
		Value:      d.Value,
		Unit:       d.Unit,
		Dimensions: toDimensionMap(d.Dimensions),
		Timestamp:  ts,
		Values:     d.Values,
		Counts:     d.Counts,
	}

	if s := d.StatisticValues; s != nil {
		datum.StatisticValues = &mondriver.StatisticSet{
			SampleCount: s.SampleCount, Sum: s.Sum, Minimum: s.Minimum, Maximum: s.Maximum,
		}
	}

	return datum
}
