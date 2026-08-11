package guardduty

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveDetectorRoot routes the /detector subtree. rest is the path below
// "detector".
func (h *Handler) serveDetectorRoot(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createDetector(w, r)
		case http.MethodGet:
			h.listDetectors(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	detectorID := rest[0]

	if len(rest) == 1 {
		h.serveDetectorByID(w, r, detectorID)

		return
	}

	h.serveDetectorSub(w, r, detectorID, rest[1:])
}

// serveDetectorByID handles GET (GetDetector), POST (UpdateDetector), and
// DELETE (DeleteDetector) on /detector/{id}.
func (h *Handler) serveDetectorByID(w http.ResponseWriter, r *http.Request, detectorID string) {
	switch r.Method {
	case http.MethodGet:
		h.getDetector(w, r, detectorID)
	case http.MethodPost:
		h.updateDetector(w, r, detectorID)
	case http.MethodDelete:
		h.deleteDetector(w, r, detectorID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createDetector(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	var req createDetectorRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

			return
		}
	}

	if req.Enable == nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "enable is required")

		return
	}

	det, err := h.gd.CreateDetector(r.Context(), driver.CreateDetectorInput{
		Enable:                     *req.Enable,
		FindingPublishingFrequency: req.FindingPublishingFrequency,
		Features:                   req.Features,
		DataSources:                req.DataSources,
		Tags:                       req.Tags,
		ClientToken:                req.ClientToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"detectorId": det.ID})
}

func (h *Handler) getDetector(w http.ResponseWriter, r *http.Request, detectorID string) {
	det, err := h.gd.GetDetector(r.Context(), detectorID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, detectorToWire(det))
}

func (h *Handler) updateDetector(w http.ResponseWriter, r *http.Request, detectorID string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	var req updateDetectorRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid JSON: "+err.Error())

			return
		}
	}

	err := h.gd.UpdateDetector(r.Context(), driver.UpdateDetectorInput{
		DetectorID:                 detectorID,
		Enable:                     req.Enable,
		FindingPublishingFrequency: req.FindingPublishingFrequency,
		Features:                   req.Features,
		DataSources:                req.DataSources,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteDetector(w http.ResponseWriter, r *http.Request, detectorID string) {
	if err := h.gd.DeleteDetector(r.Context(), detectorID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listDetectors(w http.ResponseWriter, r *http.Request) {
	ids, next, err := h.gd.ListDetectors(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if ids == nil {
		ids = []string{}
	}

	writeJSON(w, withNext(map[string]any{"detectorIds": ids}, next))
}

// serveDetectorSub routes /detector/{id}/{sub}/... to the owning resource
// handler. Direct child resources (ipset, threatintelset, filter, …) dispatch
// here; the remaining sub-resources route via serveDetectorSubresource.
func (h *Handler) serveDetectorSub(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	switch rest[0] {
	case "ipset":
		h.serveIPSet(w, r, detectorID, rest[1:])
	case "threatintelset":
		h.serveThreatIntelSet(w, r, detectorID, rest[1:])
	case "threatentityset":
		h.serveThreatEntitySet(w, r, detectorID, rest[1:])
	case "trustedentityset":
		h.serveTrustedEntitySet(w, r, detectorID, rest[1:])
	case "filter":
		h.serveFilter(w, r, detectorID, rest[1:])
	default:
		h.serveDetectorSubresource(w, r, detectorID, rest)
	}
}
