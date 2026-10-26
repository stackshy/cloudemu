package cloudwatchlogs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

type testMetricFilterRequest struct {
	FilterPattern    string   `json:"filterPattern"`
	LogEventMessages []string `json:"logEventMessages"`
}

type metricFilterMatchJSON struct {
	EventNumber     int64             `json:"eventNumber"`
	EventMessage    string            `json:"eventMessage"`
	ExtractedValues map[string]string `json:"extractedValues"`
}

type testMetricFilterResponse struct {
	Matches []metricFilterMatchJSON `json:"matches"`
}

// testMetricFilter runs a filter pattern over sample messages
// (Logs_20140328.TestMetricFilter).
func (h *Handler) testMetricFilter(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "TestMetricFilter")
	if !ok {
		return
	}

	var req testMetricFilterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	matches, err := ext.TestMetricFilter(r.Context(), req.FilterPattern, req.LogEventMessages)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := testMetricFilterResponse{Matches: make([]metricFilterMatchJSON, 0, len(matches))}
	for _, m := range matches {
		resp.Matches = append(resp.Matches, metricFilterMatchJSON{
			EventNumber: m.EventNumber, EventMessage: m.EventMessage, ExtractedValues: m.ExtractedValues,
		})
	}

	wire.WriteJSON(w, resp)
}
