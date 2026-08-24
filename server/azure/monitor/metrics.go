package monitor

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	metricsSuffix     = "/providers/microsoft.insights/metrics"
	metricDefsSuffix  = "/providers/microsoft.insights/metricdefinitions"
	defaultRegion     = "eastus"
	defaultIntervalS  = 60
	defaultIntervalPT = "PT1M"
)

// MetricsHandler serves the microsoft.insights data plane
// (Microsoft.Insights/metrics and metricDefinitions) — an extension resource
// hanging off any resource URI. It reads the timeseries the compute/storage
// mocks pushed into the monitoring driver.
type MetricsHandler struct {
	mon mondriver.Monitoring
}

// NewMetricsHandler returns a metrics data-plane handler backed by m.
func NewMetricsHandler(m mondriver.Monitoring) *MetricsHandler {
	return &MetricsHandler{mon: m}
}

// Matches claims GETs on {resourceUri}/providers/microsoft.insights/metrics and
// .../metricDefinitions. The match is on the lowercased path suffix, so it wins
// over the underlying resource's own handler regardless of which provider the
// resource belongs to.
func (*MetricsHandler) Matches(r *http.Request) bool {
	p := strings.ToLower(r.URL.Path)

	return strings.HasSuffix(p, metricsSuffix) || strings.HasSuffix(p, metricDefsSuffix)
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "GET required")
		return
	}

	p := strings.ToLower(r.URL.Path)

	switch {
	case strings.HasSuffix(p, metricDefsSuffix):
		h.listDefinitions(w, r)
	case strings.HasSuffix(p, metricsSuffix):
		h.listMetrics(w, r)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown metrics path")
	}
}

// resourceURI returns the ARM resource id the metrics hang off (the path with
// the trailing /providers/microsoft.insights/<leaf> removed).
func resourceURI(path, suffix string) string {
	idx := strings.Index(strings.ToLower(path), suffix)
	if idx < 0 {
		return strings.TrimPrefix(path, "/")
	}

	return strings.Trim(path[:idx], "/")
}

// namespaceFor derives the metric namespace: the explicit metricnamespace query
// wins, otherwise it is the provider/type of the resource URI (e.g.
// "Microsoft.Compute/virtualMachines"), which is exactly the namespace the
// compute mock stores its datapoints under.
func namespaceFor(r *http.Request, uri string) string {
	if ns := r.URL.Query().Get("metricnamespace"); ns != "" {
		return ns
	}

	return namespaceFromURI(uri)
}

func namespaceFromURI(uri string) string {
	const key = "/providers/"

	i := strings.LastIndex(strings.ToLower(uri), key)
	if i < 0 {
		return ""
	}

	const providerTypePair = 2

	parts := strings.Split(strings.Trim(uri[i+len(key):], "/"), "/")
	if len(parts) < providerTypePair {
		return ""
	}

	return parts[0] + "/" + parts[1]
}

func (h *MetricsHandler) listMetrics(w http.ResponseWriter, r *http.Request) {
	uri := resourceURI(r.URL.Path, metricsSuffix)
	namespace := namespaceFor(r, uri)
	aggs := aggregations(r.URL.Query().Get("aggregation"))
	names := splitCSV(r.URL.Query().Get("metricnames"))

	value := make([]map[string]any, 0, len(names))
	for _, name := range names {
		value = append(value, h.metricEntry(r.Context(), uri, namespace, name, aggs))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"timespan":       r.URL.Query().Get("timespan"),
		"interval":       defaultIntervalPT,
		"value":          value,
		"namespace":      namespace,
		"resourceregion": defaultRegion,
	})
}

// metricEntry builds one Metric object with a single timeseries whose datapoints
// carry each requested aggregation.
func (h *MetricsHandler) metricEntry(ctx context.Context, uri, namespace, name string, aggs []string) map[string]any {
	data := h.timeseriesData(ctx, namespace, name, aggs)

	return map[string]any{
		"id":         uri + metricsSuffix + "/" + name,
		"type":       "Microsoft.Insights/metrics",
		"name":       localizable(name),
		"unit":       "Count",
		"timeseries": []map[string]any{{"metadatavalues": []any{}, "data": data}},
		"errorCode":  "Success",
	}
}
