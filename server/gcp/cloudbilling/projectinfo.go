package cloudbilling

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// serveProjectBillingInfo dispatches /v1/projects/{project}/billingInfo.
func (h *Handler) serveProjectBillingInfo(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getBillingInfo(w, rt)
	case http.MethodPut:
		h.updateBillingInfo(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// getBillingInfo returns a project's billing linkage. A project with no linkage
// yet reports a well-formed, billing-disabled record, as real GCP does.
func (h *Handler) getBillingInfo(w http.ResponseWriter, rt route) {
	h.mu.RLock()
	pi, ok := h.projectInfo[rt.project]
	h.mu.RUnlock()

	if !ok {
		gcprest.WriteJSON(w, http.StatusOK, defaultProjectBillingInfo(rt.project))
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, pi)
}

// updateBillingInfo links (or unlinks) a project to a billing account.
// billingEnabled is derived from the target account being open.
func (h *Handler) updateBillingInfo(w http.ResponseWriter, r *http.Request, rt route) {
	var in projectBillingInfo
	if !gcprest.DecodeJSON(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	enabled, ok := h.resolveBillingEnabled(in.BillingAccountName)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid",
			"billing account not found: "+in.BillingAccountName)
		return
	}

	pi := &projectBillingInfo{
		Name:               "projects/" + rt.project + "/billingInfo",
		ProjectID:          rt.project,
		BillingAccountName: in.BillingAccountName,
		BillingEnabled:     enabled,
	}
	h.projectInfo[rt.project] = pi

	gcprest.WriteJSON(w, http.StatusOK, pi)
}

// resolveBillingEnabled reports whether linking to name enables billing. An
// empty name unlinks the project (disabled). ok is false when name references a
// non-existent account.
func (h *Handler) resolveBillingEnabled(name string) (enabled, ok bool) {
	if name == "" {
		return false, true
	}

	id := strings.TrimPrefix(name, billingAccountsName)

	acct, found := h.accounts[id]
	if !found {
		return false, false
	}

	return acct.Open, true
}

func defaultProjectBillingInfo(project string) *projectBillingInfo {
	return &projectBillingInfo{
		Name:           "projects/" + project + "/billingInfo",
		ProjectID:      project,
		BillingEnabled: false,
	}
}
