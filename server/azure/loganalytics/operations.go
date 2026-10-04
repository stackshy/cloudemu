package loganalytics

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateWorkspace maps Workspaces.CreateOrUpdate onto the logging
// driver: create when absent, otherwise apply the request's mutable fields
// (retention, tags) via UpdateLogGroup, per ARM PUT semantics, so the caller's
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

	key := workspaceKey(rp)

	if _, err := h.workspace(r.Context(), rp); err == nil {
		info, uerr := h.logs.UpdateLogGroup(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}

		meta := h.meta.upsert(key, info.ResourceID, req.Location, req.skuName(), req.settings())
		azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(info, meta))

		return
	}

	info, err := h.logs.CreateLogGroup(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	meta := h.meta.upsert(key, info.ResourceID, req.Location, req.skuName(), req.settings())
	azurearm.WriteJSON(w, http.StatusCreated, toWorkspaceJSON(info, meta))
}

func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.workspace(r.Context(), rp)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	meta := h.meta.get(workspaceKey(rp), info.ResourceID)
	azurearm.WriteJSON(w, http.StatusOK, toWorkspaceJSON(info, meta))
}

// deleteWorkspace removes the workspace. Workspaces.Delete is an LRO in the SDK;
// returning 200 with an empty body completes the poller on the first response. A
// missing workspace makes the ARM DELETE idempotent: 204 No Content ("Resource
// does not exist"), not a 404 error body.
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.deleteLogGroup(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		if cerrors.IsNotFound(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	h.meta.delete(workspaceKey(rp))
	h.children.deleteWorkspace(workspaceKey(rp))

	w.WriteHeader(http.StatusOK)
}

// PurgeResourceGroup deletes every workspace recorded under the resource
// group, with its metadata and child resources, backing the resource-group
// cascade. Groups with no scope (such as the FunctionAppLogs sink) are never
// selected.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	infos, err := h.logs.ListLogGroups(ctx, scope.Scope{})
	if err != nil {
		return err
	}

	var errs []error

	for i := range infos {
		if !infos[i].Scope.InResourceGroup(subscription, resourceGroup) {
			continue
		}

		sc := infos[i].Scope
		if err := h.deleteLogGroup(ctx, sc.Subscription, sc.ResourceGroup, infos[i].Name); err != nil &&
			!cerrors.IsNotFound(err) {
			errs = append(errs, err)
			continue
		}

		key := scopedKey(sc.Subscription, sc.ResourceGroup, infos[i].Name)
		h.meta.delete(key)
		h.children.deleteWorkspace(key)
	}

	return errors.Join(errs...)
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
		sc := infos[i].Scope
		meta := h.meta.get(scopedKey(sc.Subscription, sc.ResourceGroup, infos[i].Name), infos[i].ResourceID)
		out = append(out, toWorkspaceJSON(&infos[i], meta))
	}

	azurearm.WriteJSON(w, http.StatusOK, workspaceListResult{Value: out})
}

// scopedLogGroups is the per-resource-group workspace lookup the Azure Log
// Analytics provider offers. Workspace names are unique only within a resource
// group, so the wire resolves through it when the backend supports it.
type scopedLogGroups interface {
	GetLogGroupScoped(ctx context.Context, subscription, resourceGroup, name string) (*logdriver.LogGroupInfo, error)
	DeleteLogGroupScoped(ctx context.Context, subscription, resourceGroup, name string) error
}

// workspace returns the workspace the request path names, scoped to its
// subscription and resource group.
func (h *Handler) workspace(ctx context.Context, rp *azurearm.ResourcePath) (*logdriver.LogGroupInfo, error) {
	if s, ok := h.logs.(scopedLogGroups); ok {
		return s.GetLogGroupScoped(ctx, rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	}

	info, err := h.logs.GetLogGroup(ctx, rp.ResourceName)
	if err != nil {
		return nil, err
	}

	if !info.Scope.InResourceGroup(rp.Subscription, rp.ResourceGroup) {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", rp.ResourceName)
	}

	return info, nil
}

func (h *Handler) deleteLogGroup(ctx context.Context, subscription, resourceGroup, name string) error {
	if s, ok := h.logs.(scopedLogGroups); ok {
		return s.DeleteLogGroupScoped(ctx, subscription, resourceGroup, name)
	}

	return h.logs.DeleteLogGroup(ctx, name)
}

// workspaceKey keys the wire-layer metadata and child resources of the
// workspace the request path names.
func workspaceKey(rp *azurearm.ResourcePath) string {
	return scopedKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)
}

func scopedKey(subscription, resourceGroup, name string) string {
	return strings.ToLower(subscription) + "/" + strings.ToLower(resourceGroup) + "/" + name
}
