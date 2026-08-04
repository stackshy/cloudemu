package lambda

import (
	"net/http"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

type eventSourceMappingJSON struct {
	UUID             string `json:"UUID"`
	EventSourceArn   string `json:"EventSourceArn"`
	FunctionArn      string `json:"FunctionArn,omitempty"`
	BatchSize        int    `json:"BatchSize,omitempty"`
	State            string `json:"State,omitempty"`
	StartingPosition string `json:"StartingPosition,omitempty"`
	LastModified     string `json:"LastModified,omitempty"`
}

func toESMJSON(info *sdrv.EventSourceMappingInfo) eventSourceMappingJSON {
	return eventSourceMappingJSON{
		UUID:             info.UUID,
		EventSourceArn:   info.EventSourceArn,
		FunctionArn:      info.FunctionName,
		BatchSize:        info.BatchSize,
		State:            info.State,
		StartingPosition: info.StartingPosition,
		LastModified:     info.CreatedAt,
	}
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
	case http.MethodDelete:
		if err := h.fn.DeleteEventSourceMapping(r.Context(), uuid); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
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

func (h *Handler) listEventSourceMappings(w http.ResponseWriter, r *http.Request) {
	// FunctionName is an optional filter carried as a query parameter.
	infos, err := h.fn.ListEventSourceMappings(r.Context(), r.URL.Query().Get("FunctionName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]eventSourceMappingJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toESMJSON(&infos[i]))
	}

	writeJSON(w, http.StatusOK, map[string]any{"EventSourceMappings": out})
}
