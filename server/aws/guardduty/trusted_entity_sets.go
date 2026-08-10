package guardduty //nolint:dupl // near-identical to the sibling list-set handler files by API shape.

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveTrustedEntitySet routes /detector/{id}/trustedentityset[/{setId}].
//
//nolint:dupl // near-identical routing/create to the sibling list-set handlers by API shape.
func (h *Handler) serveTrustedEntitySet(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createTrustedEntitySet(w, r, detectorID)
		case http.MethodGet:
			h.listTrustedEntitySets(w, r, detectorID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	setID := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getTrustedEntitySet(w, r, detectorID, setID)
	case http.MethodPost:
		h.updateTrustedEntitySet(w, r, detectorID, setID)
	case http.MethodDelete:
		h.deleteTrustedEntitySet(w, r, detectorID, setID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTrustedEntitySet(w http.ResponseWriter, r *http.Request, detectorID string) {
	var req createSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	id, err := h.gd.CreateTrustedEntitySet(r.Context(), driver.CreateTrustedEntitySetInput{
		DetectorID:          detectorID,
		Name:                req.Name,
		Format:              req.Format,
		Location:            req.Location,
		Activate:            req.Activate,
		ExpectedBucketOwner: req.ExpectedBucketOwner,
		Tags:                req.Tags,
		ClientToken:         req.ClientToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"trustedEntitySetId": id})
}

func (h *Handler) getTrustedEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	s, err := h.gd.GetTrustedEntitySet(r.Context(), detectorID, setID)
	if err != nil {
		writeErr(w, err)

		return
	}

	base := setToWire(s.Name, s.Format, s.Location, s.Status, s.ExpectedBucketOwner, s.Tags)
	writeJSON(w, entitySetTimestamps(base, s.CreatedAt, s.UpdatedAt, s.ErrorDetails))
}

//nolint:dupl // near-identical update handler to the sibling list-set resources by API shape.
func (h *Handler) updateTrustedEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	var req updateSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	err := h.gd.UpdateTrustedEntitySet(r.Context(), driver.UpdateTrustedEntitySetInput{
		DetectorID:          detectorID,
		TrustedEntitySetID:  setID,
		Name:                req.Name,
		Location:            req.Location,
		Activate:            req.Activate,
		ExpectedBucketOwner: req.ExpectedBucketOwner,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteTrustedEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	if err := h.gd.DeleteTrustedEntitySet(r.Context(), detectorID, setID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTrustedEntitySets(w http.ResponseWriter, r *http.Request, detectorID string) {
	ids, next, err := h.gd.ListTrustedEntitySets(r.Context(), detectorID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if ids == nil {
		ids = []string{}
	}

	writeJSON(w, withNext(map[string]any{"trustedEntitySetIds": ids}, next))
}
