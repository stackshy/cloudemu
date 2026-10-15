package cloudids

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// Vertex AI serves the same /v1/projects/{p}/locations/{l}/endpoints paths.
// When both are mounted, IDS claims only its own traffic and Vertex gets the
// rest, including the 404 for an endpoint nobody owns.

// SetSharedPath turns on the shared endpoints rules. Off (the default), IDS
// claims every endpoints path, as a standalone server must.
func (h *Handler) SetSharedPath() { h.shared = true }

// matchesShared decides an endpoints path when Vertex AI is mounted too.
func (h *Handler) matchesShared(r *http.Request, rt *route) bool {
	if !h.shared || sharedpath.Is(r, sharedpath.IntrusionDetection) {
		return true
	}

	if sharedpath.Yield(r, sharedpath.IntrusionDetection, sharedpath.AIPlatform) {
		return false
	}

	ctx := r.Context()

	if rt.name != "" {
		_, err := h.db.GetEndpoint(ctx, rt.project, rt.location, rt.name)

		return err == nil
	}

	switch r.Method {
	case http.MethodPost:
		if id := r.URL.Query().Get(endpointIDParam); id != "" {
			if _, err := h.db.GetEndpoint(ctx, rt.project, rt.location, id); err == nil {
				return true // an IDS-owned id, so IDS answers 409
			}
		}

		return bodyLooksLikeEndpoint(r)
	case http.MethodGet:
		all, err := h.db.ListEndpoints(ctx, rt.project, rt.location)

		return err == nil && len(all) > 0
	default:
		return false
	}
}

// bodyLooksLikeEndpoint reports whether a create body is an IDS Endpoint: it has no
// displayName (which a Vertex Endpoint requires) and one of the fields IDS
// requires, severity or network. The body is restored.
func bodyLooksLikeEndpoint(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := wire.PeekBody(r, maxBodyBytes)

	if err != nil {
		return false
	}

	var probe struct {
		Severity    json.RawMessage `json:"severity"`
		Network     string          `json:"network"`
		DisplayName *string         `json:"displayName"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	return probe.DisplayName == nil && (len(probe.Severity) > 0 || probe.Network != "")
}
