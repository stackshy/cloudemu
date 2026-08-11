package guardduty

import "net/http"

// servePublishing routes /detector/{id}/publishingDestination and its
// per-destination sub-path.
func (h *Handler) servePublishing(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			body, err := h.gd.CreatePublishingDestination(ctx, id, rawBody(r))
			h.writeResult(w, body, err)
		case http.MethodGet:
			body, err := h.gd.ListPublishingDestinations(ctx, id, pageFromQuery(r))
			h.writeResult(w, body, err)
		default:
			methodNotAllowed(w)
		}

		return
	}

	destID := rest[0]

	switch r.Method {
	case http.MethodGet:
		body, err := h.gd.DescribePublishingDestination(ctx, id, destID)
		h.writeResult(w, body, err)
	case http.MethodPost:
		body, err := h.gd.UpdatePublishingDestination(ctx, id, destID, rawBody(r))
		h.writeResult(w, body, err)
	case http.MethodDelete:
		body, err := h.gd.DeletePublishingDestination(ctx, id, destID)
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}
