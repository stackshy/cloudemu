package loganalytics

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// createOrUpdateWorkspace maps Workspaces.CreateOrUpdate onto the logging
// driver's log-group create. It is idempotent: if the workspace already exists
// it is echoed back (the driver has no update primitive for retention/tags, so
// this mirrors CreateOrUpdate's upsert contract for the fields we model).
func (h *Handler) createOrUpdateWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req workspaceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	if info, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName); err == nil {
		azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(rp, info, req.Location))
		return
	}

	info, err := h.logs.CreateLogGroup(r.Context(), logdriver.LogGroupConfig{
		Name:          rp.ResourceName,
		RetentionDays: req.retentionDays(),
		Tags:          req.Tags,
	})
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
	infos, err := h.logs.ListLogGroups(r.Context())
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
