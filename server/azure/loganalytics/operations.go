package loganalytics

import (
	"github.com/stackshy/cloudemu/v2/services/scope"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// createOrUpdateWorkspace maps Workspaces.CreateOrUpdate onto the logging
// driver: create when absent, otherwise apply the request's mutable fields
// (retention, tags) via UpdateLogGroup — ARM PUT semantics, so the caller's
// changes are never silently discarded.
func (h *Handler) createOrUpdateWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg := logdriver.LogGroupConfig{
		Name:          rp.ResourceName,
		RetentionDays: req.retentionDays(),
		Tags:          req.Tags,
		Scope:         scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	if _, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName); err == nil {
		info, uerr := h.logs.UpdateLogGroup(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}
		azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(rp, info, req.Location))
		return
	}

	info, err := h.logs.CreateLogGroup(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toWorkspaceJSON(rp, info, req.Location))
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(rp, info, ""))
}

// deleteWorkspace removes the workspace. Workspaces.Delete is an LRO in the SDK;
// returning 200 with an empty body completes the poller on the first response.
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.logs.DeleteLogGroup(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.logs.ListLogGroups(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]workspaceJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toWorkspaceJSON(rp, &infos[i], ""))
	}

	azurearm.WriteJSON(w, http.StatusOK, workspaceListResult{Value: out})
}
