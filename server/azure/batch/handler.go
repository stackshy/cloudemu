// Package batch serves the Azure Batch ARM API (Microsoft.Batch/batchAccounts
// plus the nested Microsoft.Batch/batchAccounts/{account}/pools child resource).
// Real armbatch BatchAccountClient / PoolClient requests hit this handler the
// same way they hit management.azure.com.
//
// Real Azure runs account and pool CreateOrUpdate/Delete as long-running
// operations; the emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire. The pool
// allocationState state machine (Steady <-> Resizing, driven by the resize /
// stopResize actions) is modeled in the backing mock.
package batch

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/batch"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName   = "Microsoft.Batch"
	accountType    = "batchAccounts"
	poolSegment    = "pools"
	accountArmType = providerName + "/" + accountType
	poolArmType    = accountArmType + "/" + poolSegment

	actionListKeys       = "listKeys"
	actionRegenerateKey  = "regenerateKey"
	actionSyncAutoStore  = "syncAutoStorageKeys"
	actionResize         = "resize"
	actionStopResize     = "stopResize"
	actionDisableAutoScl = "disableAutoScale"
)

// Store is the minimal batch backend the handler needs. *batch.Mock satisfies it.
type Store interface {
	CreateOrUpdateAccount(
		ctx context.Context, sub, rg, name, location string, in *batch.AccountInput,
	) (batch.Account, bool, error)
	GetAccount(ctx context.Context, sub, rg, name string) (batch.Account, error)
	DeleteAccount(ctx context.Context, sub, rg, name string) (bool, error)
	ListAccountsByResourceGroup(ctx context.Context, sub, rg string) ([]batch.Account, error)
	ListAccountsBySubscription(ctx context.Context, sub string) ([]batch.Account, error)
	ListKeys(ctx context.Context, sub, rg, name string) (batch.AccountKeys, error)
	RegenerateKey(ctx context.Context, sub, rg, name, keyName string) (batch.AccountKeys, error)
	AccountExists(ctx context.Context, sub, rg, name string) bool
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	CreateOrUpdatePool(
		ctx context.Context, sub, rg, account, name string, in *batch.PoolInput,
	) (batch.Pool, bool, error)
	GetPool(ctx context.Context, sub, rg, account, name string) (batch.Pool, error)
	DeletePool(ctx context.Context, sub, rg, account, name string) (bool, error)
	ListPoolsByAccount(ctx context.Context, sub, rg, account string) ([]batch.Pool, error)
	Resize(
		ctx context.Context, sub, rg, account, name string, targetDedicated, targetLowPriority *int,
	) (batch.Pool, error)
	StopResize(ctx context.Context, sub, rg, account, name string) (batch.Pool, error)
}

// Handler serves Microsoft.Batch/batchAccounts (and nested pools) ARM requests.
type Handler struct {
	store Store
}

// New returns a batch handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a batch ARM URL. The provider and type are
// matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, accountType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A "pools" sub-resource routes to the nested pool surface; any other
	// sub-resource on the account is one of its POST actions.
	if rp.SubResource != "" {
		if strings.EqualFold(rp.SubResource, poolSegment) {
			h.servePool(w, r, &rp)
			return
		}

		h.serveAccountAction(w, r, &rp)

		return
	}

	h.serveAccount(w, r, &rp)
}

// PurgeResourceGroup deletes every batch account and pool under sub/rg so a
// resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveAccount routes the top-level account surface.
func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		h.listAccounts(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createAccount(w, r, rp)
	case http.MethodPatch:
		h.updateAccount(w, r, rp)
	case http.MethodGet:
		h.getAccount(w, r, rp)
	case http.MethodDelete:
		h.deleteAccount(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveAccountAction routes the POST sub-resource actions on a named account.
func (h *Handler) serveAccountAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	dispatchAction(w, r, rp, rp.SubResource, []actionRoute{
		{actionListKeys, h.listKeys},
		{actionRegenerateKey, h.regenerateKey},
		{actionSyncAutoStore, h.syncAutoStorageKeys},
	})
}

// servePool routes the nested pool surface, including the resize / stopResize
// POST actions.
func (h *Handler) servePool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceAction != "" {
		h.servePoolAction(w, r, rp)
		return
	}

	if rp.SubResourceName == "" {
		h.listPools(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPool(w, r, rp)
	case http.MethodPatch:
		h.updatePool(w, r, rp)
	case http.MethodGet:
		h.getPool(w, r, rp)
	case http.MethodDelete:
		h.deletePool(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// servePoolAction routes the POST sub-resource actions on a named pool. The
// disableAutoScale action is a no-op that returns the current pool: the pool is
// already fixed-scale, so there is nothing to disable.
func (h *Handler) servePoolAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	dispatchAction(w, r, rp, rp.SubResourceAction, []actionRoute{
		{actionResize, h.resizePool},
		{actionStopResize, h.stopResizePool},
		{actionDisableAutoScl, h.getPool},
	})
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req accountRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := accountInputFromRequest(&req)

	a, created, err := h.store.CreateOrUpdateAccount(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toAccountResponse(&a))
}

// updateAccount applies an ARM PATCH: only the supplied fields are overlaid onto
// the stored account; the immutable location and computed fields are preserved. A
// PATCH on a missing account is a 404.
func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.GetAccount(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req accountRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := accountInputFromRequest(&req)

	a, _, err := h.store.CreateOrUpdateAccount(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccountResponse(&a))
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	a, err := h.store.GetAccount(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccountResponse(&a))
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteAccount(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []batch.Account
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListAccountsByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListAccountsBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := accountListResponse{Value: make([]accountResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toAccountResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	keys, err := h.store.ListKeys(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccountKeysResponse(&keys))
}

func (h *Handler) regenerateKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req regenerateKeyRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	keys, err := h.store.RegenerateKey(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.KeyName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccountKeysResponse(&keys))
}

// syncAutoStorageKeys is a no-op action that returns 204 No Content, matching a
// real successful synchronize on an account whose auto-storage is unset.
func (h *Handler) syncAutoStorageKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if !h.store.AccountExists(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "batch account "+rp.ResourceName+" not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req poolRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := poolInputFromRequest(&req)

	p, created, err := h.store.CreateOrUpdatePool(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, &in)
	if err != nil {
		writePoolErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toPoolResponse(&p))
}

// updatePool applies an ARM PATCH to a pool, preserving everything the body did
// not name. A PATCH on a missing pool is a 404.
func (h *Handler) updatePool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.store.GetPool(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req poolRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := poolInputFromRequest(&req)

	p, _, err := h.store.CreateOrUpdatePool(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPoolResponse(&p))
}

func (h *Handler) getPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	p, err := h.store.GetPool(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPoolResponse(&p))
}

func (h *Handler) deletePool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeletePool(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listPools(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	items, err := h.store.ListPoolsByAccount(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := poolListResponse{Value: make([]poolResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toPoolResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) resizePool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req resizeRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	dedicated, lowPriority := resizeTargets(&req)

	p, err := h.store.Resize(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, dedicated, lowPriority)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPoolResponse(&p))
}

func (h *Handler) stopResizePool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	p, err := h.store.StopResize(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPoolResponse(&p))
}

// actionRoute pairs a POST action name with the handler that serves it.
type actionRoute struct {
	name string
	fn   func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath)
}

// dispatchAction routes a POST sub-resource action to its handler by
// case-insensitive name, writing a 405 for a non-POST method and a 404 for an
// unknown action. Both the account and pool action surfaces share it.
func dispatchAction(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, action string, routes []actionRoute,
) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	for _, rt := range routes {
		if strings.EqualFold(action, rt.name) {
			rt.fn(w, r, rp)
			return
		}
	}

	azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+action)
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the
// resource existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writePoolErr maps a pool create error, translating a missing parent account
// (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writePoolErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}
