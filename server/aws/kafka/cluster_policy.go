package kafka

import (
	"net/http"
)

// routeClusterPolicy dispatches /v1/clusters/{arn}/policy. PUT sets the policy,
// GET reads it, DELETE clears it.
func (h *Handler) routeClusterPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodPut:
		h.putClusterPolicy(w, r, arn)
	case http.MethodGet:
		h.getClusterPolicy(w, r, arn)
	case http.MethodDelete:
		h.deleteClusterPolicy(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putClusterPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	version, err := h.k.PutClusterPolicy(r.Context(), arn, body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"currentVersion": version})
}

func (h *Handler) getClusterPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	version, policy, err := h.k.GetClusterPolicy(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := map[string]any{"policy": policy}
	if version != "" {
		out["currentVersion"] = version
	}

	writeJSON(w, out)
}

func (h *Handler) deleteClusterPolicy(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.k.DeleteClusterPolicy(r.Context(), arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}
