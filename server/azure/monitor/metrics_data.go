package monitor

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// wideWindowYears bounds the query window generously so every stored datapoint
// (backfilled around a VM's launch time under a fake clock) is captured.
const wideWindowYears = 50

// aggregationKey pairs an Azure aggregation name with the driver statistic and
// the JSON field it renders as.
type aggregationKey struct {
	azure string // "average", "minimum", "maximum", "total", "count"
	stat  string // driver Stat
	field string // JSON field in a MetricValue
}

//nolint:gochecknoglobals // static lookup table
var aggregationTable = map[string]aggregationKey{
	"average": {azure: "average", stat: "Average", field: "average"},
	"minimum": {azure: "minimum", stat: "Minimum", field: "minimum"},
	"maximum": {azure: "maximum", stat: "Maximum", field: "maximum"},
	"total":   {azure: "total", stat: "Sum", field: "total"},
	"count":   {azure: "count", stat: "SampleCount", field: "count"},
}

// aggregations parses the comma-separated aggregation query, defaulting to
// average when none is supplied.
func aggregations(raw string) []string {
	names := splitCSV(strings.ToLower(raw))
	if len(names) == 0 {
		return []string{"average"}
	}

	out := make([]string, 0, len(names))

	for _, n := range names {
		if _, ok := aggregationTable[n]; ok {
			out = append(out, n)
		}
	}

	if len(out) == 0 {
		return []string{"average"}
	}

	return out
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}

	return out
}

func localizable(v string) map[string]string {
	return map[string]string{"value": v, "localizedValue": v}
}

// timeseriesData builds the datapoint array for one metric, one row per bucket
// timestamp, carrying every requested aggregation. Buckets are keyed by
// timestamp so multiple aggregation queries align.
func (h *MetricsHandler) timeseriesData(ctx context.Context, namespace, name string, aggs []string) []map[string]any {
	start := time.Unix(0, 0)
	end := time.Now().AddDate(wideWindowYears, 0, 0)

	order := []time.Time{}
	rows := map[time.Time]map[string]any{}

	for _, agg := range aggs {
		key := aggregationTable[agg]
		res, err := h.mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  namespace,
			MetricName: name,
			StartTime:  start,
			EndTime:    end,
			Period:     defaultIntervalS,
			Stat:       key.stat,
		})

		if err != nil || res == nil {
			continue
		}

		for i, ts := range res.Timestamps {
			row, ok := rows[ts]
			if !ok {
				row = map[string]any{"timeStamp": ts.UTC().Format(time.RFC3339)}
				rows[ts] = row

				order = append(order, ts)
			}

			row[key.field] = res.Values[i]
		}
	}

	return orderedRows(order, rows)
}

func orderedRows(order []time.Time, rows map[time.Time]map[string]any) []map[string]any {
	sortTimes(order)

	out := make([]map[string]any, 0, len(order))
	for _, ts := range order {
		out = append(out, rows[ts])
	}

	return out
}

func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

// listDefinitions serves metricDefinitions: one definition per metric name the
// driver knows in the resource's namespace.
func (h *MetricsHandler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	uri := resourceURI(r.URL.Path, metricDefsSuffix)
	namespace := namespaceFor(r, uri)

	names, err := h.mon.ListMetrics(r.Context(), namespace)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	value := make([]map[string]any, 0, len(names))
	for _, name := range names {
		value = append(value, definitionEntry(uri, namespace, name))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

func definitionEntry(uri, namespace, name string) map[string]any {
	return map[string]any{
		"id":                        uri + metricDefsSuffix + "/" + name,
		"resourceId":                uri,
		"namespace":                 namespace,
		"name":                      localizable(name),
		"unit":                      "Count",
		"primaryAggregationType":    "Average",
		"supportedAggregationTypes": []string{"Average", "Minimum", "Maximum", "Total", "Count"},
		"metricAvailabilities":      []map[string]any{{"timeGrain": defaultIntervalPT, "retention": "P93D"}},
	}
}
