package guardduty

import (
	"encoding/json"
	"net/http"
)

// This file routes the detector sub-resource operations, whose driver methods
// return the response body as raw JSON (built to the SDK wire shape) so the
// handler can write it verbatim.

// writeResult writes a driver call's outcome: the mapped error on failure, an
// empty JSON object when the body is nil, or the raw JSON body verbatim.
func (h *Handler) writeResult(w http.ResponseWriter, body json.RawMessage, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	if body == nil {
		writeJSON(w, map[string]any{})

		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(body)
}

// rawBody reads the request body for a driver op that takes raw JSON. Returns
// nil on read failure; the driver then sees an empty body and validates it.
func rawBody(r *http.Request) json.RawMessage {
	b, err := readAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	if err != nil {
		return nil
	}

	return b
}

// serveDetectorSubresource routes the detector sub-resources: members, findings,
// org config, publishing, coverage, usage, malware-scan settings/scans,
// free-trial, and the administrator/master relationship.
//
//nolint:gocyclo // one arm per detector sub-resource; large by API design.
func (h *Handler) serveDetectorSubresource(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	ctx := r.Context()

	switch rest[0] {
	case "administrator":
		h.serveAdministrator(w, r, detectorID, rest[1:])
	case "master":
		h.serveMaster(w, r, detectorID, rest[1:])
	case "member":
		h.serveMember(w, r, detectorID, rest[1:])
	case "findings":
		h.serveDetectorFindings(w, r, detectorID, rest[1:])
	case "admin":
		h.serveOrgConfig(w, r, detectorID)
	case "publishingDestination":
		h.servePublishing(w, r, detectorID, rest[1:])
	case "coverage":
		h.serveDetectorCoverage(w, r, detectorID, rest[1:])
	case "usage":
		body, err := h.gd.GetUsageStatistics(ctx, detectorID, rawBody(r))
		h.writeResult(w, body, err)
	case "malware-scan-settings":
		h.serveDetectorMalwareSettings(w, r, detectorID)
	case "malware-scans":
		body, err := h.gd.DescribeMalwareScans(ctx, detectorID, rawBody(r))
		h.writeResult(w, body, err)
	case "freeTrial":
		body, err := h.gd.GetRemainingFreeTrialDays(ctx, detectorID, rawBody(r))
		h.writeResult(w, body, err)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

//nolint:gocyclo // one arm per findings sub-path; large by API design.
func (h *Handler) serveDetectorFindings(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		body, err := h.gd.ListFindings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)

		return
	}

	switch rest[0] {
	case "archive":
		body, err := h.gd.ArchiveFindings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "unarchive":
		body, err := h.gd.UnarchiveFindings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "create":
		body, err := h.gd.CreateSampleFindings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "get":
		body, err := h.gd.GetFindings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "statistics":
		body, err := h.gd.GetFindingsStatistics(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "feedback":
		body, err := h.gd.UpdateFindingsFeedback(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) serveDetectorCoverage(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 1 && rest[0] == "statistics" {
		body, err := h.gd.GetCoverageStatistics(ctx, id, rawBody(r))
		h.writeResult(w, body, err)

		return
	}

	body, err := h.gd.ListCoverage(ctx, id, rawBody(r))
	h.writeResult(w, body, err)
}

func (h *Handler) serveDetectorMalwareSettings(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		body, err := h.gd.GetMalwareScanSettings(ctx, id)
		h.writeResult(w, body, err)
	case http.MethodPost:
		body, err := h.gd.UpdateMalwareScanSettings(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}
