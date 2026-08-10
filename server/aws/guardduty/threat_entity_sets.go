package guardduty

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveThreatEntitySet routes /detector/{id}/threatentityset[/{setId}].
func (h *Handler) serveThreatEntitySet(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createThreatEntitySet(w, r, detectorID)
		case http.MethodGet:
			h.listThreatEntitySets(w, r, detectorID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	setID := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getThreatEntitySet(w, r, detectorID, setID)
	case http.MethodPost:
		h.updateThreatEntitySet(w, r, detectorID, setID)
	case http.MethodDelete:
		h.deleteThreatEntitySet(w, r, detectorID, setID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createThreatEntitySet(w http.ResponseWriter, r *http.Request, detectorID string) {
	var req createSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	id, err := h.gd.CreateThreatEntitySet(r.Context(), driver.CreateThreatEntitySetInput{
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

	writeJSON(w, map[string]any{"threatEntitySetId": id})
}

func (h *Handler) getThreatEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	s, err := h.gd.GetThreatEntitySet(r.Context(), detectorID, setID)
	if err != nil {
		writeErr(w, err)

		return
	}

	base := setToWire(s.Name, s.Format, s.Location, s.Status, s.ExpectedBucketOwner, s.Tags)
	writeJSON(w, entitySetTimestamps(base, s.CreatedAt, s.UpdatedAt, s.ErrorDetails))
}

func (h *Handler) updateThreatEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	var req updateSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	err := h.gd.UpdateThreatEntitySet(r.Context(), driver.UpdateThreatEntitySetInput{
		DetectorID:          detectorID,
		ThreatEntitySetID:   setID,
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

func (h *Handler) deleteThreatEntitySet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	if err := h.gd.DeleteThreatEntitySet(r.Context(), detectorID, setID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listThreatEntitySets(w http.ResponseWriter, r *http.Request, detectorID string) {
	ids, next, err := h.gd.ListThreatEntitySets(r.Context(), detectorID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if ids == nil {
		ids = []string{}
	}

	writeJSON(w, withNext(map[string]any{"threatEntitySetIds": ids}, next))
}
