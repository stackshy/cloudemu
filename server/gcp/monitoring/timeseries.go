package monitoring

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// seriesReader is the GCP-specific optional interface a monitoring backend
// implements to expose raw, per-label metric series for timeSeries.list. It is
// kept out of the shared driver.Monitoring interface so AWS/Azure need not
// implement it.
type seriesReader interface {
	GCPSeriesKeys() []mondriver.MetricIdentifier
	GCPRawSeries(namespace, metricName string) []mondriver.MetricDatum
}

// serveTimeSeries routes /v3/projects/{p}/timeSeries (list on GET, ingest on POST).
func (h *Handler) serveTimeSeries(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodGet:
		h.listTimeSeries(w, r)
	case http.MethodPost:
		h.createTimeSeries(w, r, project)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createTimeSeries(w http.ResponseWriter, r *http.Request, _ string) {
	var body createTimeSeriesRequest

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	if len(body.TimeSeries) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "timeSeries is required")
		return
	}

	var data []mondriver.MetricDatum

	for _, ts := range body.TimeSeries {
		ns, metric := splitMetricType(ts.Metric.Type)
		if metric == "" {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "metric.type is required")
			return
		}

		for _, p := range ts.Points {
			data = append(data, mondriver.MetricDatum{
				Namespace:  ns,
				MetricName: metric,
				Value:      pointValue(p.Value),
				Dimensions: ts.Metric.Labels,
				Timestamp:  pointTimestamp(p.Interval),
			})
		}
	}

	if err := h.mon.PutMetricData(r.Context(), data); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listTimeSeries(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.mon.(seriesReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "timeSeries.list unsupported")
		return
	}

	q := r.URL.Query()
	filter := parseSeriesFilter(q.Get("filter"))
	start := parseRFC3339(q.Get("interval.startTime"))
	end := parseRFC3339(q.Get("interval.endTime"))

	if end.IsZero() {
		end = time.Now()
	}

	out := listTimeSeriesResponse{TimeSeries: []timeSeries{}}

	for _, key := range reader.GCPSeriesKeys() {
		fullType := metricType(key.Namespace, key.MetricName)
		if !filter.matchesType(fullType) {
			continue
		}

		raw := reader.GCPRawSeries(key.Namespace, key.MetricName)
		out.TimeSeries = append(out.TimeSeries, buildSeries(fullType, raw, filter, start, end)...)
	}

	writeJSON(w, http.StatusOK, out)
}

// buildSeries groups raw datums by their label set into one timeSeries each,
// keeping only points inside [start,end] whose labels satisfy the filter.
func buildSeries(fullType string, raw []mondriver.MetricDatum, f seriesFilter, start, end time.Time) []timeSeries {
	groups := make(map[string][]mondriver.MetricDatum)
	order := []string{}

	for i := range raw {
		d := raw[i]
		if !inWindow(d.Timestamp, start, end) || !f.matchesLabels(d.Dimensions) {
			continue
		}

		k := labelKey(d.Dimensions)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}

		groups[k] = append(groups[k], d)
	}

	series := make([]timeSeries, 0, len(order))

	for _, k := range order {
		series = append(series, oneSeries(fullType, groups[k]))
	}

	return series
}

func oneSeries(fullType string, data []mondriver.MetricDatum) timeSeries {
	// Cloud Monitoring returns points newest-first.
	sort.Slice(data, func(i, j int) bool { return data[i].Timestamp.After(data[j].Timestamp) })

	pts := make([]point, 0, len(data))

	for i := range data {
		ts := data[i].Timestamp.UTC().Format(time.RFC3339)
		val := data[i].Value
		pts = append(pts, point{
			Interval: timeInterval{StartTime: ts, EndTime: ts},
			Value:    typedValue{DoubleValue: &val},
		})
	}

	labels := map[string]string{}
	if len(data) > 0 {
		labels = data[0].Dimensions
	}

	return timeSeries{
		Metric:     metricRef{Type: fullType, Labels: labels},
		Resource:   resourceFor(labels),
		MetricKind: "GAUGE",
		ValueType:  "DOUBLE",
		Points:     pts,
	}
}

// resourceFor derives a datum's monitored resource from its label set. A datum
// carrying an instance_id is a gce_instance whose resource labels are
// project_id, instance_id and zone (whichever were emitted) — the same labels
// Cloud Monitoring filters like resource.labels.zone=… match against, so a
// filtered timeSeries.list returns the series. Anything else is the global
// resource.
func resourceFor(labels map[string]string) monitoredRes {
	id, ok := labels["instance_id"]
	if !ok {
		return monitoredRes{Type: "global"}
	}

	resLabels := map[string]string{"instance_id": id}

	for _, key := range []string{"project_id", "zone"} {
		if v, ok := labels[key]; ok {
			resLabels[key] = v
		}
	}

	return monitoredRes{Type: "gce_instance", Labels: resLabels}
}

func inWindow(ts, start, end time.Time) bool {
	if !start.IsZero() && ts.Before(start) {
		return false
	}

	if !end.IsZero() && ts.After(end) {
		return false
	}

	return true
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte(';')
	}

	return b.String()
}

// metricType joins a namespace and metric name into a Cloud Monitoring
// metric.type; splitMetricType is its inverse (split at the first slash).
func metricType(namespace, metricName string) string {
	return namespace + "/" + metricName
}

func splitMetricType(t string) (namespace, metricName string) {
	i := strings.Index(t, "/")
	if i < 0 {
		return t, ""
	}

	return t[:i], t[i+1:]
}

func pointValue(v typedValue) float64 {
	if v.DoubleValue != nil {
		return *v.DoubleValue
	}

	if v.Int64Value != nil {
		f, _ := strconv.ParseFloat(*v.Int64Value, 64)

		return f
	}

	return 0
}

func pointTimestamp(iv timeInterval) time.Time {
	if t := parseRFC3339(iv.EndTime); !t.IsZero() {
		return t
	}

	if t := parseRFC3339(iv.StartTime); !t.IsZero() {
		return t
	}

	return time.Now()
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t
}
