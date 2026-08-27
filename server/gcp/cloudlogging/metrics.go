package cloudlogging

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// metricDescriptorJSON is the subset of the log-based metric's MetricDescriptor
// the SDK reads back (kind/type/unit).
type metricDescriptorJSON struct {
	MetricKind string `json:"metricKind,omitempty"`
	ValueType  string `json:"valueType,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

// logMetricJSON is the Cloud Logging LogMetric resource shape.
type logMetricJSON struct {
	Name             string                `json:"name,omitempty"`
	Description      string                `json:"description,omitempty"`
	Filter           string                `json:"filter,omitempty"`
	ValueExtractor   string                `json:"valueExtractor,omitempty"`
	MetricDescriptor *metricDescriptorJSON `json:"metricDescriptor,omitempty"`
	CreateTime       string                `json:"createTime,omitempty"`
	UpdateTime       string                `json:"updateTime,omitempty"`
}

type listMetricsResponse struct {
	Metrics       []logMetricJSON `json:"metrics"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

func toMetricJSON(m *logdriver.LogBasedMetric) logMetricJSON {
	out := logMetricJSON{
		Name:           m.Name,
		Description:    m.Description,
		Filter:         m.Filter,
		ValueExtractor: m.ValueExtractor,
	}

	if m.MetricKind != "" || m.ValueType != "" || m.Unit != "" {
		out.MetricDescriptor = &metricDescriptorJSON{
			MetricKind: m.MetricKind,
			ValueType:  m.ValueType,
			Unit:       m.Unit,
		}
	}

	if !m.CreatedAt.IsZero() {
		out.CreateTime = m.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	if !m.UpdatedAt.IsZero() {
		out.UpdateTime = m.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return out
}

func (b *logMetricJSON) toDriver(name string) *logdriver.LogBasedMetric {
	m := &logdriver.LogBasedMetric{
		Name:           name,
		Description:    b.Description,
		Filter:         b.Filter,
		ValueExtractor: b.ValueExtractor,
	}

	if b.MetricDescriptor != nil {
		m.MetricKind = b.MetricDescriptor.MetricKind
		m.ValueType = b.MetricDescriptor.ValueType
		m.Unit = b.MetricDescriptor.Unit
	}

	return m
}

// writeMetric writes a single metric or the driver error that produced it.
func writeMetric(w http.ResponseWriter, m *logdriver.LogBasedMetric, err error) {
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toMetricJSON(m))
}

//nolint:dupl // parallel-by-design with routeSinks; sibling REST collections, distinct types
func (h *Handler) routeMetrics(w http.ResponseWriter, r *http.Request, tail string) {
	gcp, ok := h.gcpBackend(w)
	if !ok {
		return
	}

	project := projectFromPath(r.URL.Path)

	if tail == "/" {
		switch r.Method {
		case http.MethodPost:
			createMetric(w, r, gcp, project)
		case http.MethodGet:
			listMetrics(w, r, gcp, project)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	metricID := strings.TrimPrefix(tail, "/")

	switch r.Method {
	case http.MethodGet:
		m, err := gcp.GetLogMetric(r.Context(), project, metricID)
		writeMetric(w, m, err)
	case http.MethodPut, http.MethodPatch:
		updateMetric(w, r, gcp, project, metricID)
	case http.MethodDelete:
		deleteResource(w, gcp.DeleteLogMetric(r.Context(), project, metricID))
	default:
		writeMethodNotAllowed(w)
	}
}

func createMetric(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project string) {
	var body logMetricJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	m, err := gcp.CreateLogMetric(r.Context(), project, body.toDriver(body.Name))
	writeMetric(w, m, err)
}

func updateMetric(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project, metricID string) {
	var body logMetricJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	m, err := gcp.UpdateLogMetric(r.Context(), project, body.toDriver(metricID))
	writeMetric(w, m, err)
}

func listMetrics(w http.ResponseWriter, r *http.Request, gcp logdriver.GCPLogging, project string) {
	metrics, err := gcp.ListLogMetrics(r.Context(), project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]logMetricJSON, 0, len(metrics))
	for i := range metrics {
		out = append(out, toMetricJSON(&metrics[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listMetricsResponse{Metrics: out})
}
