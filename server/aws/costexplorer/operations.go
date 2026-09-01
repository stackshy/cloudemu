package costexplorer

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// getCostAndUsage answers ce:GetCostAndUsage: it prices the live inventory by
// service and spreads each service's monthly estimate over the requested time
// period at the requested granularity, optionally grouped by SERVICE.
func (h *Handler) getCostAndUsage(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetCostAndUsage)
}

func (h *Handler) doGetCostAndUsage(ctx context.Context, in *getCostAndUsageInput) (any, error) {
	if in.TimePeriod == nil {
		return nil, cerrors.New(cerrors.InvalidArgument, "TimePeriod is required")
	}

	gran := granularityOrDefault(in.Granularity)

	ivs, err := buckets(*in.TimePeriod, gran)
	if err != nil {
		return nil, err
	}

	metrics := in.Metrics
	if len(metrics) == 0 {
		metrics = []string{"UnblendedCost"}
	}

	byService, err := cost.ServiceMonthly(ctx, h.inv)
	if err != nil {
		return nil, err
	}

	grouped := wantsServiceGroup(in.GroupBy)

	out := &getCostAndUsageOutput{ResultsByTime: make([]resultByTime, 0, len(ivs))}
	if grouped {
		out.GroupDefinitions = []groupDefinition{{Type: "DIMENSION", Key: "SERVICE"}}
	}

	for _, iv := range ivs {
		out.ResultsByTime = append(out.ResultsByTime, buildResult(iv, gran, metrics, byService, grouped))
	}

	return out, nil
}

// buildResult shapes one granularity bucket: grouped requests carry per-service
// Groups (and an empty Total, matching real Cost Explorer), ungrouped requests
// carry only the aggregate Total.
func buildResult(iv dateInterval, gran string, metrics []string, byService map[string]float64, grouped bool) resultByTime {
	res := resultByTime{TimePeriod: iv, Estimated: true, Total: map[string]metricValue{}}

	if grouped {
		res.Groups = groupsByService(gran, metrics, byService)

		return res
	}

	var total float64
	for _, v := range byService {
		total += v
	}

	res.Total = metricValuesFor(metrics, perBucketRate(total, gran))

	return res
}

// groupsByService builds one Group per costed service, keyed by its Cost
// Explorer SERVICE display name, in a stable order.
func groupsByService(gran string, metrics []string, byService map[string]float64) []group {
	keys := sortedKeys(byService)
	groups := make([]group, 0, len(keys))

	for _, k := range keys {
		groups = append(groups, group{
			Keys:    []string{displayName(k)},
			Metrics: metricValuesFor(metrics, perBucketRate(byService[k], gran)),
		})
	}

	return groups
}

// getCostForecast answers ce:GetCostForecast: it projects the current monthly
// spend rate forward over the requested period.
func (h *Handler) getCostForecast(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetCostForecast)
}

func (h *Handler) doGetCostForecast(ctx context.Context, in *getCostForecastInput) (any, error) {
	if in.TimePeriod == nil {
		return nil, cerrors.New(cerrors.InvalidArgument, "TimePeriod is required")
	}

	gran := forecastGranularity(in.Granularity)

	ivs, err := buckets(*in.TimePeriod, gran)
	if err != nil {
		return nil, err
	}

	byService, err := cost.ServiceMonthly(ctx, h.inv)
	if err != nil {
		return nil, err
	}

	var monthly float64
	for _, v := range byService {
		monthly += v
	}

	var total float64

	forecasts := make([]forecastResult, 0, len(ivs))

	for _, iv := range ivs {
		amt := perBucketRate(monthly, gran)
		total += amt
		forecasts = append(forecasts, forecastResult{TimePeriod: iv, MeanValue: formatAmount(amt)})
	}

	return &getCostForecastOutput{
		Total:                 metricValue{Amount: formatAmount(total), Unit: "USD"},
		ForecastResultsByTime: forecasts,
	}, nil
}

// getDimensionValues answers ce:GetDimensionValues. Only the SERVICE dimension
// is populated (from the costed inventory); other dimensions return empty.
func (h *Handler) getDimensionValues(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetDimensionValues)
}

func (h *Handler) doGetDimensionValues(ctx context.Context, in *getDimensionValuesInput) (any, error) {
	out := &getDimensionValuesOutput{DimensionValues: []dimensionValuesWithAttributes{}}

	if !strings.EqualFold(in.Dimension, "SERVICE") {
		return out, nil
	}

	byService, err := cost.ServiceMonthly(ctx, h.inv)
	if err != nil {
		return nil, err
	}

	for _, k := range sortedKeys(byService) {
		out.DimensionValues = append(out.DimensionValues, dimensionValuesWithAttributes{Value: displayName(k)})
	}

	out.ReturnSize = len(out.DimensionValues)
	out.TotalSize = out.ReturnSize

	return out, nil
}

// getTags answers ce:GetTags: the cost-allocation tag keys present in the
// inventory, or the values of a specific TagKey when one is supplied.
func (h *Handler) getTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetTags)
}

func (h *Handler) doGetTags(ctx context.Context, in *getTagsInput) (any, error) {
	res, err := h.inv.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	set := map[string]bool{}

	for i := range res {
		if in.TagKey != nil && *in.TagKey != "" {
			if v, ok := res[i].Tags[*in.TagKey]; ok && v != "" {
				set[v] = true
			}

			continue
		}

		for k := range res[i].Tags {
			set[k] = true
		}
	}

	tags := sortedStrings(set)

	return &getTagsOutput{Tags: tags, ReturnSize: len(tags), TotalSize: len(tags)}, nil
}

// metricValuesFor builds a Total/Group metric map: cost metrics carry the
// dollar amount in USD; usage metrics (which the emulator does not track)
// report zero with unit N/A.
func metricValuesFor(metrics []string, amount float64) map[string]metricValue {
	out := make(map[string]metricValue, len(metrics))

	for _, m := range metrics {
		if costMetrics[m] {
			out[m] = metricValue{Amount: formatAmount(amount), Unit: "USD"}

			continue
		}

		out[m] = metricValue{Amount: "0", Unit: "N/A"}
	}

	return out
}

// perBucketRate converts a monthly estimate to the amount for a single bucket
// at the given granularity, using AWS's 730-hours-per-month convention.
func perBucketRate(monthly float64, gran string) float64 {
	switch gran {
	case granularityHourly:
		return monthly / hoursPerMonth
	case granularityDaily:
		return monthly * 24.0 / hoursPerMonth
	default:
		return monthly
	}
}

// buckets splits [Start, End) into the granularity's intervals. A trailing
// partial interval (End not on a boundary) is clamped to End.
func buckets(iv dateInterval, gran string) ([]dateInterval, error) {
	start, err := parseDate(iv.Start)
	if err != nil {
		return nil, err
	}

	end, err := parseDate(iv.End)
	if err != nil {
		return nil, err
	}

	if !end.After(start) {
		return nil, cerrors.New(cerrors.InvalidArgument, "TimePeriod End must be after Start")
	}

	var out []dateInterval

	for cur := start; cur.Before(end); {
		next := stepBucket(cur, gran)
		if next.After(end) {
			next = end
		}

		out = append(out, dateInterval{Start: formatBucket(cur, gran), End: formatBucket(next, gran)})
		cur = next
	}

	return out, nil
}

// stepBucket advances one bucket at the given granularity.
func stepBucket(t time.Time, gran string) time.Time {
	switch gran {
	case granularityHourly:
		return t.Add(time.Hour)
	case granularityMonthly:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// formatBucket renders a boundary in the wire form Cost Explorer uses for the
// granularity: an RFC3339 timestamp for HOURLY, a date for DAILY/MONTHLY.
func formatBucket(t time.Time, gran string) string {
	if gran == granularityHourly {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}

	return t.UTC().Format(time.DateOnly)
}

// parseDate accepts both the date-only ("2006-01-02") and RFC3339 forms the SDK
// may send in a DateInterval.
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}

	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}

	return time.Time{}, cerrors.Newf(cerrors.InvalidArgument, "invalid date: %q", s)
}

// formatAmount renders a dollar amount the way Cost Explorer does: a plain
// decimal string.
func formatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', 10, 64)
}

// granularityOrDefault normalizes a requested granularity, defaulting to
// MONTHLY when unset or unrecognized.
func granularityOrDefault(g string) string {
	switch strings.ToUpper(g) {
	case granularityHourly, granularityDaily, granularityMonthly:
		return strings.ToUpper(g)
	default:
		return granularityMonthly
	}
}

// forecastGranularity restricts to the DAILY/MONTHLY granularities forecasts
// support, defaulting to MONTHLY.
func forecastGranularity(g string) string {
	if strings.EqualFold(g, granularityDaily) {
		return granularityDaily
	}

	return granularityMonthly
}

// wantsServiceGroup reports whether the request asks to group by the SERVICE
// dimension (the one grouping this handler models).
func wantsServiceGroup(defs []groupDefinition) bool {
	for _, d := range defs {
		if strings.EqualFold(d.Type, "DIMENSION") && strings.EqualFold(d.Key, "SERVICE") {
			return true
		}
	}

	return false
}

// sortedKeys returns the keys of m in ascending order.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// sortedStrings returns the members of set in ascending order.
func sortedStrings(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}
