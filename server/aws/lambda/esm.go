package lambda

import (
	"net/http"
	"sort"
	"time"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

type eventSourceMappingJSON struct {
	UUID             string  `json:"UUID"`
	EventSourceArn   string  `json:"EventSourceArn"`
	FunctionArn      string  `json:"FunctionArn,omitempty"`
	BatchSize        int     `json:"BatchSize,omitempty"`
	State            string  `json:"State,omitempty"`
	StartingPosition string  `json:"StartingPosition,omitempty"`
	LastModified     float64 `json:"LastModified,omitempty"`
}

func toESMJSON(info *sdrv.EventSourceMappingInfo) eventSourceMappingJSON {
	functionArn := info.FunctionArn
	if functionArn == "" {
		functionArn = info.FunctionName
	}

	return eventSourceMappingJSON{
		UUID:             info.UUID,
		EventSourceArn:   info.EventSourceArn,
		FunctionArn:      functionArn,
		BatchSize:        info.BatchSize,
		State:            info.State,
		StartingPosition: info.StartingPosition,
		LastModified:     esmLastModified(info.CreatedAt),
	}
}

// esmLastModified converts the stored RFC3339 timestamp to the epoch-seconds
// number the Lambda SDK expects for EventSourceMappingConfiguration.LastModified
// (a JSON number, not a string). An unparseable value is omitted (0).
func esmLastModified(ts string) float64 {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

// serveEventSourceMappings dispatches the /2015-03-31/event-source-mappings
// paths: collection (POST create, GET list) and per-UUID (GET/DELETE).
func (h *Handler) serveEventSourceMappings(w http.ResponseWriter, r *http.Request, uuid string) {
	if uuid == "" {
		switch r.Method {
		case http.MethodPost:
			h.createEventSourceMapping(w, r)
		case http.MethodGet:
			h.listEventSourceMappings(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		info, err := h.fn.GetEventSourceMapping(r.Context(), uuid)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toESMJSON(info))
	case http.MethodPut:
		h.updateEventSourceMapping(w, r, uuid)
	case http.MethodDelete:
		// AWS returns 202 with the full EventSourceMappingConfiguration whose
		// State is "Deleting" (the mapping enters a Deleting state and is not
		// fully removed for several seconds). Snapshot it before deleting so the
		// SDK caller can read UUID/State/FunctionArn off the response.
		info, err := h.fn.GetEventSourceMapping(r.Context(), uuid)
		if err != nil {
			writeErr(w, err)
			return
		}

		if err := h.fn.DeleteEventSourceMapping(r.Context(), uuid); err != nil {
			writeErr(w, err)
			return
		}

		body := toESMJSON(info)
		body.State = "Deleting"
		writeJSON(w, http.StatusAccepted, body)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

func (h *Handler) createEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventSourceArn   string `json:"EventSourceArn"`
		FunctionName     string `json:"FunctionName"`
		BatchSize        int    `json:"BatchSize"`
		Enabled          *bool  `json:"Enabled"`
		StartingPosition string `json:"StartingPosition"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	info, err := h.fn.CreateEventSourceMapping(r.Context(), sdrv.EventSourceMappingConfig{
		EventSourceArn:   req.EventSourceArn,
		FunctionName:     req.FunctionName,
		BatchSize:        req.BatchSize,
		Enabled:          enabled,
		StartingPosition: req.StartingPosition,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toESMJSON(info))
}

// updateEventSourceMapping applies the mutable fields (BatchSize, Enabled,
// FunctionName) of an existing mapping, merging onto its current state.
func (h *Handler) updateEventSourceMapping(w http.ResponseWriter, r *http.Request, uuid string) {
	var req struct {
		FunctionName string `json:"FunctionName"`
		BatchSize    int    `json:"BatchSize"`
		Enabled      *bool  `json:"Enabled"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	existing, err := h.fn.GetEventSourceMapping(r.Context(), uuid)
	if err != nil {
		writeErr(w, err)
		return
	}

	cfg := sdrv.EventSourceMappingConfig{
		EventSourceArn:   existing.EventSourceArn,
		FunctionName:     existing.FunctionName,
		BatchSize:        existing.BatchSize,
		Enabled:          existing.State != "Disabled",
		StartingPosition: existing.StartingPosition,
	}

	if req.FunctionName != "" {
		cfg.FunctionName = req.FunctionName
	}

	if req.BatchSize != 0 {
		cfg.BatchSize = req.BatchSize
	}

	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}

	info, err := h.fn.UpdateEventSourceMapping(r.Context(), uuid, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toESMJSON(info))
}

func (h *Handler) listEventSourceMappings(w http.ResponseWriter, r *http.Request) {
	// FunctionName and EventSourceArn are optional filters carried as query
	// parameters. FunctionName is applied by the driver; EventSourceArn narrows
	// the result further to mappings for a single event source.
	eventSourceArn := r.URL.Query().Get("EventSourceArn")

	infos, err := h.fn.ListEventSourceMappings(r.Context(), r.URL.Query().Get("FunctionName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	// Apply the EventSourceArn filter first, then sort by UUID so Marker
	// offsets stay stable across paginated calls.
	filtered := make([]sdrv.EventSourceMappingInfo, 0, len(infos))

	for i := range infos {
		if eventSourceArn != "" && infos[i].EventSourceArn != eventSourceArn {
			continue
		}

		filtered = append(filtered, infos[i])
	}

	sort.Slice(filtered, func(i, j int) bool { return filtered[i].UUID < filtered[j].UUID })

	start, end, nextMarker, truncated := pageWindow(len(filtered), r.URL.Query())

	out := make([]eventSourceMappingJSON, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, toESMJSON(&filtered[i]))
	}

	body := map[string]any{"EventSourceMappings": out}
	if truncated {
		body["NextMarker"] = nextMarker
	}

	writeJSON(w, http.StatusOK, body)
}
