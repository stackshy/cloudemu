package appsync

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// serveAPIKeys routes /v1/apis/{apiId}/apikeys and its item paths.
func (h *Handler) serveAPIKeys(w http.ResponseWriter, r *http.Request, apiID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createAPIKey(w, r, apiID)
		case http.MethodGet:
			h.listAPIKeys(w, r, apiID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	id := rest[0]

	switch r.Method {
	case http.MethodPost:
		h.updateAPIKey(w, r, apiID, id)
	case http.MethodDelete:
		h.deleteAPIKey(w, r, apiID, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request, apiID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body apiKeyBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	desc := ""
	if body.Description != nil {
		desc = *body.Description
	}

	out, err := h.as.CreateAPIKey(r.Context(), &driver.CreateAPIKeyInput{
		APIID:       apiID,
		Description: desc,
		Expires:     body.Expires,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"apiKey": apiKeyToWire(out)})
}

func (h *Handler) updateAPIKey(w http.ResponseWriter, r *http.Request, apiID, id string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	var body apiKeyBody
	if !unmarshalBody(w, raw, &body) {
		return
	}

	out, err := h.as.UpdateAPIKey(r.Context(), &driver.UpdateAPIKeyInput{
		APIID:       apiID,
		ID:          id,
		Description: body.Description,
		Expires:     body.Expires,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"apiKey": apiKeyToWire(out)})
}

func (h *Handler) deleteAPIKey(w http.ResponseWriter, r *http.Request, apiID, id string) {
	if err := h.as.DeleteAPIKey(r.Context(), apiID, id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request, apiID string) {
	keys, next, err := h.as.ListAPIKeys(r.Context(), apiID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(keys))
	for i := range keys {
		out = append(out, apiKeyToWire(&keys[i]))
	}

	writeJSON(w, withNext(map[string]any{"apiKeys": out}, next))
}
