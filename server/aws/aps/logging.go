package aps

import "net/http"

// serveLogging routes /workspaces/{id}/logging.
func (h *Handler) serveLogging(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodPost:
		h.createLoggingConfiguration(w, r, workspaceID)
	case http.MethodPut:
		h.updateLoggingConfiguration(w, r, workspaceID)
	case http.MethodGet:
		h.describeLoggingConfiguration(w, r, workspaceID)
	case http.MethodDelete:
		h.deleteLoggingConfiguration(w, r, workspaceID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createLoggingConfiguration(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	cfg, err := h.aps.CreateLoggingConfiguration(r.Context(), workspaceID, stringField(raw, "logGroupArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"status": statusBlock(cfg.Status)})
}

func (h *Handler) updateLoggingConfiguration(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	cfg, err := h.aps.UpdateLoggingConfiguration(r.Context(), workspaceID, stringField(raw, "logGroupArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"status": statusBlock(cfg.Status)})
}

func (h *Handler) describeLoggingConfiguration(w http.ResponseWriter, r *http.Request, workspaceID string) {
	cfg, err := h.aps.DescribeLoggingConfiguration(r.Context(), workspaceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"loggingConfiguration": loggingToWire(workspaceID, cfg)})
}

func (h *Handler) deleteLoggingConfiguration(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if err := h.aps.DeleteLoggingConfiguration(r.Context(), workspaceID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}
