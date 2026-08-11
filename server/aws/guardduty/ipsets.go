package guardduty

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveIPSet routes /detector/{id}/ipset and /detector/{id}/ipset/{ipSetId}.
//
//nolint:dupl // near-identical routing/create to the sibling list-set handlers by API shape.
func (h *Handler) serveIPSet(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createIPSet(w, r, detectorID)
		case http.MethodGet:
			h.listIPSets(w, r, detectorID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	ipSetID := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getIPSet(w, r, detectorID, ipSetID)
	case http.MethodPost:
		h.updateIPSet(w, r, detectorID, ipSetID)
	case http.MethodDelete:
		h.deleteIPSet(w, r, detectorID, ipSetID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createIPSet(w http.ResponseWriter, r *http.Request, detectorID string) {
	var req createSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	id, err := h.gd.CreateIPSet(r.Context(), driver.CreateIPSetInput{
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

	writeJSON(w, map[string]any{"ipSetId": id})
}

func (h *Handler) getIPSet(w http.ResponseWriter, r *http.Request, detectorID, ipSetID string) {
	s, err := h.gd.GetIPSet(r.Context(), detectorID, ipSetID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, setToWire(s.Name, s.Format, s.Location, s.Status, s.ExpectedBucketOwner, s.Tags))
}

//nolint:dupl // near-identical update handler to the sibling list-set resources by API shape.
func (h *Handler) updateIPSet(w http.ResponseWriter, r *http.Request, detectorID, ipSetID string) {
	var req updateSetRequest
	if !decodeInto(w, r, &req) {
		return
	}

	err := h.gd.UpdateIPSet(r.Context(), driver.UpdateIPSetInput{
		DetectorID:          detectorID,
		IPSetID:             ipSetID,
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

func (h *Handler) deleteIPSet(w http.ResponseWriter, r *http.Request, detectorID, ipSetID string) {
	if err := h.gd.DeleteIPSet(r.Context(), detectorID, ipSetID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listIPSets(w http.ResponseWriter, r *http.Request, detectorID string) {
	ids, next, err := h.gd.ListIPSets(r.Context(), detectorID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if ids == nil {
		ids = []string{}
	}

	writeJSON(w, withNext(map[string]any{"ipSetIds": ids}, next))
}

// decodeInto reads and unmarshals a JSON body into v. An empty body is treated
// as an empty object (many GuardDuty ops carry required input in the path).
func decodeInto(w http.ResponseWriter, r *http.Request, v any) bool {
	body, ok := readBody(w, r)
	if !ok {
		return false
	}

	if len(body) == 0 {
		return true
	}

	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

		return false
	}

	return true
}
