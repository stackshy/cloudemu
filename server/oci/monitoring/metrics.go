package monitoring

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// postMetricData ingests a batch of metric data, each entry naming its own
// compartment.
func (h *Handler) postMetricData(w http.ResponseWriter, r *http.Request) {
	var req postMetricDataRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if len(req.MetricData) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "metricData is required")
		return
	}

	for i := range req.MetricData {
		entry := &req.MetricData[i]

		if entry.CompartmentID == "" {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"compartmentId is required on every metricData entry")

			return
		}

		if err := h.mon.PostMetricData(r.Context(), entry.CompartmentID, entry.ResourceGroup, toData(entry)); err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}
	}

	ocirest.WriteJSON(w, r, http.StatusOK, postMetricDataResponse{FailedMetrics: []metricDataDetails{}})
}

// listMetrics returns the metric identities recorded in a compartment.
func (h *Handler) listMetrics(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	var req listMetricsDetails

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	metrics, err := h.mon.ListOCIMetrics(r.Context(), compartmentID, mondriver.OCIMetricFilter{
		Namespace:     req.Namespace,
		ResourceGroup: req.ResourceGroup,
		Name:          req.Name,
		Dimensions:    req.DimensionFilters,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	limit := ocirest.Limit(r)
	out := make([]metric, 0, len(metrics))

	for i := range metrics {
		if len(out) == limit {
			break
		}

		out = append(out, metric{
			Name:          metrics[i].Name,
			Namespace:     metrics[i].Namespace,
			ResourceGroup: metrics[i].ResourceGroup,
			CompartmentID: metrics[i].CompartmentID,
			Dimensions:    metrics[i].Dimensions,
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

// summarizeMetricsData aggregates the series a query selects.
func (h *Handler) summarizeMetricsData(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	var req summarizeMetricsDataDetails

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Query == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "query is required")
		return
	}

	metrics, err := h.mon.SummarizeOCIMetrics(r.Context(), compartmentID, mondriver.OCIMetricQuery{
		Namespace:     req.Namespace,
		ResourceGroup: req.ResourceGroup,
		Query:         req.Query,
		Resolution:    req.Resolution,
		StartTime:     at(req.StartTime),
		EndTime:       at(req.EndTime),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]metricData, 0, len(metrics))
	for i := range metrics {
		out = append(out, toMetricData(&metrics[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

// toData flattens one metricData entry into the driver's data points.
func toData(entry *metricDataDetails) []mondriver.MetricDatum {
	out := make([]mondriver.MetricDatum, 0, len(entry.Datapoints))

	for _, p := range entry.Datapoints {
		out = append(out, mondriver.MetricDatum{
			Namespace:  entry.Namespace,
			MetricName: entry.Name,
			Value:      p.Value,
			Unit:       entry.Metadata["unit"],
			Dimensions: entry.Dimensions,
			Timestamp:  p.Timestamp,
		})
	}

	return out
}

func toMetricData(m *mondriver.OCIMetric) metricData {
	points := make([]aggregatedDatapoint, 0, len(m.Timestamps))

	for i, ts := range m.Timestamps {
		points = append(points, aggregatedDatapoint{Timestamp: timestamp(ts), Value: m.Values[i]})
	}

	return metricData{
		Namespace:            m.Namespace,
		ResourceGroup:        m.ResourceGroup,
		CompartmentID:        m.CompartmentID,
		Name:                 m.Name,
		Dimensions:           m.Dimensions,
		Resolution:           m.Resolution,
		AggregatedDatapoints: points,
	}
}

func at(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}

	return *t
}
