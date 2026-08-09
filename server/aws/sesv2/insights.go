package sesv2

import (
	"net/http"
)

// serveMetrics routes /metrics/batch (BatchGetMetricData).
func (h *Handler) serveMetrics(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || rest[0] != "batch" || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req batchGetMetricDataRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ids := make([]string, 0, len(req.Queries))
	for _, q := range req.Queries {
		ids = append(ids, q.ID)
	}

	data, err := h.ses.BatchGetMetricData(r.Context(), ids)
	if err != nil {
		writeErr(w, err)

		return
	}

	results := make([]metricDataResultJSON, 0, len(ids))
	for _, id := range ids {
		results = append(results, metricDataResultJSON{ID: id, Values: data[id]})
	}

	writeJSON(w, batchGetMetricDataResponse{Results: results})
}

// serveInsights routes /insights/{MessageID} (GetMessageInsights).
func (h *Handler) serveInsights(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || r.Method != http.MethodGet {
		notFound(w, r.URL.Path)

		return
	}

	msg, err := h.ses.GetMessageInsights(r.Context(), rest[0])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getMessageInsightsResponse{
		MessageID:        msg.MessageID,
		FromEmailAddress: msg.FromAddress,
		Insights:         []any{},
	})
}

// serveAddrInsights routes /email-address-insights (GetEmailAddressInsights).
func (h *Handler) serveAddrInsights(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req addrInsightsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	blob, err := h.ses.GetEmailAddressInsights(r.Context(), req.EmailAddress)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRawJSON(w, blob)
}

// serveVDM routes /vdm/recommendations (ListRecommendations).
func (h *Handler) serveVDM(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || rest[0] != "recommendations" || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	if _, err := h.ses.ListRecommendations(r.Context()); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listRecommendationsResponse{Recommendations: []any{}})
}
