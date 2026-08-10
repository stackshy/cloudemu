package vpclattice

import (
	"net/http"
	"strings"
)

// serveAuthPolicy routes PUT/GET/DELETE /authpolicy/{resourceIdentifier}. The
// identifier is the whole remainder (it may be an ARN containing slashes).
func (h *Handler) serveAuthPolicy(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	resourceID := strings.Join(rest, "/")

	switch r.Method {
	case http.MethodPut:
		h.putAuthPolicy(w, r, resourceID)
	case http.MethodGet:
		h.getAuthPolicy(w, r, resourceID)
	case http.MethodDelete:
		h.deleteAuthPolicy(w, r, resourceID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putAuthPolicy(w http.ResponseWriter, r *http.Request, resourceID string) {
	var req struct {
		Policy string `json:"policy"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	p, err := h.lattice.PutAuthPolicy(r.Context(), resourceID, req.Policy)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"policy": p.Policy, "state": p.State})
}

func (h *Handler) getAuthPolicy(w http.ResponseWriter, r *http.Request, resourceID string) {
	p, err := h.lattice.GetAuthPolicy(r.Context(), resourceID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"policy": p.Policy, "state": p.State, "createdAt": p.CreatedAt, "lastUpdatedAt": p.LastUpdatedAt,
	})
}

func (h *Handler) deleteAuthPolicy(w http.ResponseWriter, r *http.Request, resourceID string) {
	if err := h.lattice.DeleteAuthPolicy(r.Context(), resourceID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// serveResourcePolicy routes PUT/GET/DELETE /resourcepolicy/{resourceArn}.
func (h *Handler) serveResourcePolicy(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFound(w, r.URL.Path)

		return
	}

	arn := strings.Join(rest, "/")

	switch r.Method {
	case http.MethodPut:
		h.putResourcePolicy(w, r, arn)
	case http.MethodGet:
		h.getResourcePolicy(w, r, arn)
	case http.MethodDelete:
		h.deleteResourcePolicy(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		Policy string `json:"policy"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.lattice.PutResourcePolicy(r.Context(), arn, req.Policy); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) getResourcePolicy(w http.ResponseWriter, r *http.Request, arn string) {
	policy, err := h.lattice.GetResourcePolicy(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"policy": policy})
}

func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.lattice.DeleteResourcePolicy(r.Context(), arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}
