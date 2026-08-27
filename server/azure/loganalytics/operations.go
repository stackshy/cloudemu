package loganalytics

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateWorkspace maps Workspaces.CreateOrUpdate onto the logging
// driver: create when absent, otherwise apply the request's mutable fields
// (retention, tags) via UpdateLogGroup — ARM PUT semantics, so the caller's
// changes are never silently discarded. The Azure-only fields (location, sku)
// and the assigned customerId GUID are tracked in the wire handler's metadata.
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

		meta := h.meta.upsert(rp.ResourceName, info.ResourceID, req.Location, req.skuName())
		azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(info, meta))

		return
	}

	info, err := h.logs.CreateLogGroup(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	meta := h.meta.upsert(rp.ResourceName, info.ResourceID, req.Location, req.skuName())
	azurearm.WriteJSON(w, http.StatusCreated, toWorkspaceJSON(info, meta))
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.logs.GetLogGroup(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	meta := h.meta.get(rp.ResourceName, info.ResourceID)
	azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(info, meta))
}

// deleteWorkspace removes the workspace. Workspaces.Delete is an LRO in the SDK;
// returning 200 with an empty body completes the poller on the first response. A
// missing workspace makes the ARM DELETE idempotent: 204 No Content ("Resource
// does not exist"), not a 404 error body.
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.logs.DeleteLogGroup(r.Context(), rp.ResourceName); err != nil {
		if cerrors.IsNotFound(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	h.meta.delete(rp.ResourceName)
	h.children.deleteWorkspace(rp.ResourceName)

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
		meta := h.meta.get(infos[i].Name, infos[i].ResourceID)
		out = append(out, toWorkspaceJSON(&infos[i], meta))
	}

	azurearm.WriteJSON(w, http.StatusOK, workspaceListResult{Value: out})
}
