package guardduty

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveThreatIntelSet routes /detector/{id}/threatintelset[/{setId}].
//
//nolint:dupl // near-identical routing/create to the sibling list-set handlers by API shape.
func (h *Handler) serveThreatIntelSet(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createThreatIntelSet(w, r, detectorID)
		case http.MethodGet:
			h.listThreatIntelSets(w, r, detectorID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	setID := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getThreatIntelSet(w, r, detectorID, setID)
	case http.MethodPost:
		h.updateThreatIntelSet(w, r, detectorID, setID)
	case http.MethodDelete:
		h.deleteThreatIntelSet(w, r, detectorID, setID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createThreatIntelSet(w http.ResponseWriter, r *http.Request, detectorID string) {
	var req createSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	id, err := h.gd.CreateThreatIntelSet(r.Context(), driver.CreateThreatIntelSetInput{
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

	writeJSON(w, map[string]any{"threatIntelSetId": id})
}

func (h *Handler) getThreatIntelSet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	s, err := h.gd.GetThreatIntelSet(r.Context(), detectorID, setID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, setToWire(s.Name, s.Format, s.Location, s.Status, s.ExpectedBucketOwner, s.Tags))
}

//nolint:dupl // near-identical update handler to the sibling list-set resources by API shape.
func (h *Handler) updateThreatIntelSet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	var req updateSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	err := h.gd.UpdateThreatIntelSet(r.Context(), driver.UpdateThreatIntelSetInput{
		DetectorID:          detectorID,
		ThreatIntelSetID:    setID,
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

func (h *Handler) deleteThreatIntelSet(w http.ResponseWriter, r *http.Request, detectorID, setID string) {
	if err := h.gd.DeleteThreatIntelSet(r.Context(), detectorID, setID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listThreatIntelSets(w http.ResponseWriter, r *http.Request, detectorID string) {
	ids, next, err := h.gd.ListThreatIntelSets(r.Context(), detectorID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if ids == nil {
		ids = []string{}
	}

	writeJSON(w, withNext(map[string]any{"threatIntelSetIds": ids}, next))
}
