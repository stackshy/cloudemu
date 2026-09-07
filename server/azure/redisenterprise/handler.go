// Package redisenterprise serves the Azure Redis Enterprise ARM API
// (Microsoft.Cache/redisEnterprise plus the nested
// Microsoft.Cache/redisEnterprise/{cluster}/databases child resource). Real
// armredisenterprise RedisEnterpriseClient / DatabasesClient requests hit this
// handler the same way they hit management.azure.com.
//
// This is distinct from the standard Azure Cache for Redis
// (Microsoft.Cache/redis) served by the azurecache handler — the two share the
// Microsoft.Cache namespace but claim different resource types, so registration
// order is unconstrained.
//
// Real Azure runs cluster and database CreateOrUpdate/Delete as long-running
// operations; the emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire.
package redisenterprise

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/redisenterprise"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName    = "Microsoft.Cache"
	clusterType     = "redisEnterprise"
	databaseSegment = "databases"
	clusterArmType  = providerName + "/" + clusterType
	databaseArmType = clusterArmType + "/" + databaseSegment
)

// Store is the minimal redisEnterprise backend the handler needs.
// *redisenterprise.Mock satisfies it.
type Store interface {
	CreateOrUpdateCluster(
		ctx context.Context, sub, rg, name, location string, in *redisenterprise.ClusterInput,
	) (redisenterprise.Cluster, bool, error)
	GetCluster(ctx context.Context, sub, rg, name string) (redisenterprise.Cluster, error)
	DeleteCluster(ctx context.Context, sub, rg, name string) (bool, error)
	ListClustersByResourceGroup(ctx context.Context, sub, rg string) ([]redisenterprise.Cluster, error)
	ListClustersBySubscription(ctx context.Context, sub string) ([]redisenterprise.Cluster, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	CreateOrUpdateDatabase(
		ctx context.Context, sub, rg, cluster, name string, in *redisenterprise.DatabaseInput,
	) (redisenterprise.Database, bool, error)
	GetDatabase(ctx context.Context, sub, rg, cluster, name string) (redisenterprise.Database, error)
	DeleteDatabase(ctx context.Context, sub, rg, cluster, name string) (bool, error)
	ListDatabasesByCluster(ctx context.Context, sub, rg, cluster string) ([]redisenterprise.Database, error)
}

// Handler serves Microsoft.Cache/redisEnterprise (and nested databases) ARM
// requests.
type Handler struct {
	store Store
}

// New returns a redisEnterprise handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a redisEnterprise ARM URL. The provider and
// type are matched case-insensitively; matching the redisEnterprise resource type
// (not the shared Microsoft.Cache namespace) keeps this distinct from the standard
// Azure Cache for Redis (Microsoft.Cache/redis) handler.
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

	// A "databases" sub-resource routes to the nested database surface; any other
	// sub-resource is unknown (clusters expose no POST actions in scope).
	if rp.SubResource != "" {
		if strings.EqualFold(rp.SubResource, databaseSegment) {
			h.serveDatabase(w, r, &rp)
			return
		}

		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown sub-resource "+rp.SubResource)

		return
	}

	h.serveCluster(w, r, &rp)
}

// PurgeResourceGroup deletes every redisEnterprise cluster and database under
// sub/rg so a resource-group delete cascades into them.
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

// serveDatabase routes the nested database surface, including the listKeys /
// regenerateKey POST actions.
func (h *Handler) serveDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceAction != "" {
		h.serveDatabaseAction(w, r, rp)
		return
	}

	if rp.SubResourceName == "" {
		h.listDatabases(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createDatabase(w, r, rp)
	case http.MethodPatch:
		h.updateDatabase(w, r, rp)
	case http.MethodGet:
		h.getDatabase(w, r, rp)
	case http.MethodDelete:
		h.deleteDatabase(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveDatabaseAction routes the POST sub-resource actions on a named database.
func (h *Handler) serveDatabaseAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch rp.SubResourceAction {
	case "listKeys", "regenerateKey":
		// Keys are deterministic and stable; a regenerate returns the same keys.
		h.listKeys(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResourceAction)
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
	// deleted one returns 200 OK. The armredisenterprise client accepts both.
	writeDeleteStatus(w, existed)
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []redisenterprise.Cluster
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

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req databaseRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := databaseInputFromRequest(&req)

	d, created, err := h.store.CreateOrUpdateDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, &in)
	if err != nil {
		writeDatabaseErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toDatabaseResponse(&d))
}

// updateDatabase applies an ARM PATCH to a database, preserving everything the
// body did not name. A PATCH on a missing database is a 404.
func (h *Handler) updateDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.store.GetDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req databaseRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := databaseInputFromRequest(&req)

	d, _, err := h.store.CreateOrUpdateDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toDatabaseResponse(&d))
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	d, err := h.store.GetDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toDatabaseResponse(&d))
}

func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListDatabasesByCluster(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := databaseListResponse{Value: make([]databaseResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toDatabaseResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	d, err := h.store.GetDatabase(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toKeysResponse(&d))
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the resource
// existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeDatabaseErr maps a database create error, translating a missing parent
// cluster (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writeDatabaseErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}

// clusterInputFromRequest builds a cluster create/update Input from a request
// body. Pointer fields are carried through so an absent field falls back to the
// stored (or default) value in the driver, which makes a PATCH body merge on its
// own.
func clusterInputFromRequest(req *clusterRequest) redisenterprise.ClusterInput {
	in := redisenterprise.ClusterInput{
		Tags:     req.Tags,
		Sku:      toDriverSku(req.Sku),
		Zones:    req.Zones,
		Identity: toDriverIdentity(req.Identity),
	}

	if req.Properties != nil {
		in.MinimumTLSVersion = req.Properties.MinimumTLSVersion
	}

	return in
}

// databaseInputFromRequest builds a database create/update Input from a request
// body.
func databaseInputFromRequest(req *databaseRequest) redisenterprise.DatabaseInput {
	var in redisenterprise.DatabaseInput

	if req.Properties == nil {
		return in
	}

	p := req.Properties
	in.ClientProtocol = p.ClientProtocol
	in.ClusteringPolicy = p.ClusteringPolicy
	in.EvictionPolicy = p.EvictionPolicy
	in.Port = p.Port
	in.Modules = toDriverModules(p.Modules)

	if p.GeoReplication != nil {
		nickname := p.GeoReplication.GroupNickname
		in.GroupNickname = &nickname
	}

	return in
}
