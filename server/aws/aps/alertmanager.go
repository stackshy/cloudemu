package aps

import "net/http"

// serveAlertManager routes /workspaces/{id}/alertmanager/definition.
func (h *Handler) serveAlertManager(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodPost:
		h.createAlertManagerDefinition(w, r, workspaceID)
	case http.MethodPut:
		h.putAlertManagerDefinition(w, r, workspaceID)
	case http.MethodGet:
		h.describeAlertManagerDefinition(w, r, workspaceID)
	case http.MethodDelete:
		h.deleteAlertManagerDefinition(w, r, workspaceID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createAlertManagerDefinition(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	def, err := h.aps.CreateAlertManagerDefinition(r.Context(), workspaceID, stringField(raw, "data"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"status": statusBlock(def.Status)})
}

func (h *Handler) putAlertManagerDefinition(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	def, err := h.aps.PutAlertManagerDefinition(r.Context(), workspaceID, stringField(raw, "data"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"status": statusBlock(def.Status)})
}

func (h *Handler) describeAlertManagerDefinition(w http.ResponseWriter, r *http.Request, workspaceID string) {
	def, err := h.aps.DescribeAlertManagerDefinition(r.Context(), workspaceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"alertManagerDefinition": alertManagerToWire(def)})
}

func (h *Handler) deleteAlertManagerDefinition(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if err := h.aps.DeleteAlertManagerDefinition(r.Context(), workspaceID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}
