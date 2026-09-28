package vpc

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// setLabelsAction is the custom verb for POST .../addresses/{name}/setLabels
// (regional) and .../global/addresses/{name}/setLabels.
const setLabelsAction = "setLabels"

// labelsFilterPrefix is the field prefix a compute list filter uses to match a
// label value, e.g. `labels.env=prod`.
const labelsFilterPrefix = "labels."

// filterOpNotEqual is the inequality operator a compute list filter uses.
const filterOpNotEqual = "!="

// addressSetLabelsRequest is the RegionSetLabelsRequest / GlobalSetLabelsRequest
// body. Both carry the same two fields.
type addressSetLabelsRequest struct {
	Labels           map[string]string `json:"labels"`
	LabelFingerprint string            `json:"labelFingerprint"`
}

// addressLabels extracts the labels map from a stored address body.
func addressLabels(body json.RawMessage) map[string]string {
	var withLabels struct {
		Labels map[string]string `json:"labels"`
	}

	_ = json.Unmarshal(body, &withLabels)

	return withLabels.Labels
}

// setAddressLabels handles setLabels on a regional or global address. The
// request's labels REPLACE the whole set; the caller must send the current
// labelFingerprint (read from a Get), and a missing or stale one is rejected
// 412 conditionNotMet with no change applied. The check-and-replace is the
// provider's (driver.GCPAddressStore.SetGCPAddressLabels), so the labels are
// part of the snapshot. Success returns a DONE compute Operation recorded in
// the shared registry, and a later Get shows the new labels under a new
// labelFingerprint.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setAddressLabels(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req addressSetLabelsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if h.addresses.store == nil {
		writeAddressErr(w, errAddressesUnsupported)
		return
	}

	err := h.addresses.store.SetGCPAddressLabels(r.Context(), rp.Project, scopeOf(rp), rp.ResourceName,
		req.Labels, req.LabelFingerprint)

	switch {
	case err == nil:
	case cerrors.IsNotFound(err):
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "address "+rp.ResourceName+" not found")
		return
	case cerrors.IsFailedPrecondition(err):
		gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet", cerrors.Message(err))
		return
	default:
		writeAddressErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project,
		rp.Scope, rp.ScopeName, resourceAddresses, rp.ResourceName, setLabelsAction))
}

// addressMatches applies a compute list filter to a stored address. It extends
// the shared name-only matcher with `labels.<key>=<value>` (and `!=`, `eq`,
// `ne`) so a label-scoped list returns only the addresses carrying that label.
// Any other field falls through to the shared matcher, which matches by name
// and treats fields it does not understand as match-all.
func addressMatches(filter string, body json.RawMessage) bool {
	f := strings.TrimSpace(filter)
	if !strings.HasPrefix(f, labelsFilterPrefix) {
		return nameMatches(filter, rawName(body))
	}

	for _, cand := range []string{filterOpNotEqual, "=", " ne ", " eq "} {
		idx := strings.Index(f, cand)
		if idx < 0 {
			continue
		}

		key := strings.TrimPrefix(strings.TrimSpace(f[:idx]), labelsFilterPrefix)
		want := strings.Trim(strings.TrimSpace(f[idx+len(cand):]), `"'`)
		op := strings.TrimSpace(cand)
		negate := op == filterOpNotEqual || op == "ne"

		got, has := addressLabels(body)[key]

		return (has && got == want) != negate
	}

	return true
}
