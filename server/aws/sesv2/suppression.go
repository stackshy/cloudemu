package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveSuppression routes /suppression/addresses and its sub-paths.
func (h *Handler) serveSuppression(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 || rest[0] != "addresses" {
		notFound(w, r.URL.Path)

		return
	}

	switch len(rest) {
	case 1:
		switch r.Method {
		case http.MethodGet:
			h.listSuppressed(w, r)
		case http.MethodPut:
			h.putSuppressed(w, r)
		default:
			methodNotAllowed(w)
		}
	case twoSegments:
		switch r.Method {
		case http.MethodGet:
			h.getSuppressed(w, r, rest[1])
		case http.MethodDelete:
			h.deleteSuppressed(w, r, rest[1])
		default:
			methodNotAllowed(w)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) putSuppressed(w http.ResponseWriter, r *http.Request) {
	var req putSuppressedDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.ses.PutSuppressedDestination(r.Context(), driver.PutSuppressedInput{
		EmailAddress: req.EmailAddress,
		Reason:       req.Reason,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) getSuppressed(w http.ResponseWriter, r *http.Request, addr string) {
	s, err := h.ses.GetSuppressedDestination(r.Context(), addr)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getSuppressedDestinationResponse{
		SuppressedDestination: suppressedDestinationJSON{
			EmailAddress:   s.EmailAddress,
			Reason:         s.Reason,
			LastUpdateTime: epochSeconds(s.LastUpdateTime),
		},
	})
}

func (h *Handler) deleteSuppressed(w http.ResponseWriter, r *http.Request, addr string) {
	if err := h.ses.DeleteSuppressedDestination(r.Context(), addr); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listSuppressed(w http.ResponseWriter, r *http.Request) {
	items, err := h.ses.ListSuppressedDestinations(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]suppressedSummaryJSON, 0, len(items))
	for i := range items {
		out = append(out, suppressedSummaryJSON{
			EmailAddress:   items[i].EmailAddress,
			Reason:         items[i].Reason,
			LastUpdateTime: epochSeconds(items[i].LastUpdateTime),
		})
	}

	writeJSON(w, listSuppressedDestinationsResponse{SuppressedDestinationSummaries: out})
}
