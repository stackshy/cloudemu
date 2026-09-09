// Package mongocluster serves the Azure Cosmos DB for MongoDB (vCore) ARM API
// (Microsoft.DocumentDB/mongoClusters), including the listConnectionStrings POST
// action. Real armmongocluster MongoClustersClient requests hit this handler the
// same way they hit management.azure.com.
//
// This is distinct from the Cosmos DB core service
// (Microsoft.DocumentDB/databaseAccounts) served by the cosmosdb handler — the two
// share the Microsoft.DocumentDB namespace but claim different resource types, so
// registration order is unconstrained.
//
// Real Azure runs cluster CreateOrUpdate/Delete as long-running operations; the
// emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire.
package mongocluster

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/mongocluster"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName   = "Microsoft.DocumentDB"
	clusterType    = "mongoClusters"
	clusterArmType = providerName + "/" + clusterType

	actionListConnectionStrings = "listConnectionStrings"
)

// Store is the minimal mongo-cluster backend the handler needs.
// *mongocluster.Mock satisfies it.
type Store interface {
	CreateOrUpdateCluster(
		ctx context.Context, sub, rg, name, location string, in *mongocluster.ClusterInput,
	) (mongocluster.Cluster, bool, error)
	GetCluster(ctx context.Context, sub, rg, name string) (mongocluster.Cluster, error)
	DeleteCluster(ctx context.Context, sub, rg, name string) (bool, error)
	ListClustersByResourceGroup(ctx context.Context, sub, rg string) ([]mongocluster.Cluster, error)
	ListClustersBySubscription(ctx context.Context, sub string) ([]mongocluster.Cluster, error)
	ListConnectionStrings(ctx context.Context, sub, rg, name string) ([]mongocluster.ConnectionStringEntry, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.DocumentDB/mongoClusters ARM requests.
type Handler struct {
	store Store
}

// New returns a mongo-cluster handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a mongoClusters ARM URL. The provider and type
// are matched case-insensitively; matching the mongoClusters resource type (not the
// shared Microsoft.DocumentDB namespace) keeps this distinct from the Cosmos DB
// core (Microsoft.DocumentDB/databaseAccounts) handler.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, clusterType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// The only sub-resource in scope is the listConnectionStrings POST action;
	// any other is unknown (firewall rules are out of scope).
	if rp.SubResource != "" {
		if strings.EqualFold(rp.SubResource, actionListConnectionStrings) {
			h.listConnectionStrings(w, r, &rp)
			return
		}

		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown sub-resource "+rp.SubResource)

		return
	}

	h.serveCluster(w, r, &rp)
}

// PurgeResourceGroup deletes every mongo cluster under sub/rg so a resource-group
// delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveCluster routes the top-level cluster surface.
func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listClusters(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createCluster(w, r, rp)
	case http.MethodPatch:
		h.updateCluster(w, r, rp)
	case http.MethodGet:
		h.getCluster(w, r, rp)
	case http.MethodDelete:
		h.deleteCluster(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req clusterRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := clusterInputFromRequest(&req)

	c, created, err := h.store.CreateOrUpdateCluster(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toClusterResponse(&c))
}

// updateCluster applies an ARM PATCH: only the supplied fields are overlaid onto
// the stored cluster; the immutable location and computed fields are preserved. A
// PATCH on a missing cluster is a 404.
func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.GetCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req clusterRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := clusterInputFromRequest(&req)

	c, _, err := h.store.CreateOrUpdateCluster(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toClusterResponse(&c))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	c, err := h.store.GetCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toClusterResponse(&c))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armmongocluster client accepts both.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []mongocluster.Cluster
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListClustersByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListClustersBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := clusterListResponse{Value: make([]clusterResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toClusterResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// listConnectionStrings serves the POST listConnectionStrings action, returning the
// cluster's stable connection strings.
func (h *Handler) listConnectionStrings(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListConnectionStrings(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toConnectionStringsResponse(items))
}
