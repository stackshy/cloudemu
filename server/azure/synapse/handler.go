// Package synapse serves the Azure Synapse Analytics ARM control-plane API
// (Microsoft.Synapse/workspaces and their sqlPools, bigDataPools and
// integrationRuntimes). Real azure-sdk-for-go armsynapse clients drive this
// surface the same way they hit management.azure.com.
//
// Workspaces own their child pools and runtimes; deleting a workspace cascades
// to all of them. The SDK's workspace create/update/delete, SQL-pool
// create/delete/pause/resume, Spark-pool create/delete and integration-runtime
// create/delete/start/stop are Begin* pollers. This handler answers them
// synchronously — a create returns 201/200 with provisioningState already
// "Succeeded" and no Azure-AsyncOperation/Location header, a delete returns
// 200/204, and an action returns 200 — so the poller terminates on its first
// poll and never hangs. This mirrors the Event Hubs and Container Apps
// control-plane handlers.
//
// Synapse has no ARM-reachable data plane here: SQL/Spark query execution is out
// of scope. This handler is control-plane only, and is self-contained with no
// backing driver (its state is workspace-scoped ARM containers, like Event
// Hubs), so it is always registered.
package synapse

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName    = "Microsoft.Synapse"
	typeWorkspaces  = "workspaces"
	childSQLPools   = "sqlPools"
	childBigData    = "bigDataPools"
	childIntRuntime = "integrationRuntimes"

	actionPause  = "pause"
	actionResume = "resume"
	actionStart  = "start"
	actionStop   = "stop"
)

// Handler serves ARM Synapse requests. It owns the in-memory workspace tree.
type Handler struct {
	mu         sync.RWMutex
	workspaces *memstore.Store[*workspaceState]
}

// New returns a Synapse control-plane handler.
func New() *Handler {
	return &Handler{workspaces: memstore.New[*workspaceState]()}
}

// Matches reports whether r targets a Microsoft.Synapse/workspaces URL.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)

	return ok && strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, typeWorkspaces)
}

// ServeHTTP routes a request by the parsed ARM path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case rp.ResourceName == "":
		h.listWorkspaces(w, r, &rp)
	case rp.SubResource == "":
		h.serveWorkspace(w, r, &rp)
	default:
		h.serveChild(w, r, &rp)
	}
}

// serveChild dispatches the child-resource routes under a workspace.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch {
	case strings.EqualFold(rp.SubResource, childSQLPools):
		h.serveSQLPool(w, r, rp)
	case strings.EqualFold(rp.SubResource, childBigData):
		h.serveBigDataPool(w, r, rp)
	case strings.EqualFold(rp.SubResource, childIntRuntime):
		h.serveIntRuntime(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unsupported Synapse sub-resource")
	}
}

// PurgeResourceGroup deletes every Synapse workspace under sub/rg (and its
// children, which the workspace state owns), so a resource-group delete cascades
// into them (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(_ context.Context, subscription, resourceGroup string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	all := h.workspaces.All()
	for k := range all {
		if strings.EqualFold(all[k].Subscription, subscription) &&
			strings.EqualFold(all[k].ResourceGroup, resourceGroup) {
			h.workspaces.Delete(k)
		}
	}

	return nil
}

// wsKey is the case-insensitive store key for a workspace; workspace names are
// unique within a subscription+resource group.
func wsKey(sub, rg, name string) string {
	return strings.ToLower(sub + "/" + rg + "/" + name)
}

// getWorkspace returns the workspace matching the request scope, or false.
// Callers hold h.mu.
func (h *Handler) getWorkspace(rp *azurearm.ResourcePath) (*workspaceState, bool) {
	return h.workspaces.Get(wsKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName))
}

// writeParentNotFound reports a missing workspace for a child request.
func writeParentNotFound(w http.ResponseWriter, ws string) {
	azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound", "workspace not found: "+ws)
}

// deleteStatus writes the ARM idempotent-delete status: 200 when a resource was
// removed, 204 when it was already absent. Begin* delete pollers accept both.
func deleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// createStatus is 201 for a new resource, 200 for an in-place update.
func createStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}
