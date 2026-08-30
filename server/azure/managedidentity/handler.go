// Package managedidentity serves the Azure User-Assigned Managed Identities ARM
// API (Microsoft.ManagedIdentity/userAssignedIdentities). Real armmsi
// UserAssignedIdentitiesClient requests hit this handler the same way they hit
// management.azure.com.
//
// Every operation is synchronous (sync-200/201): the armmsi client's
// CreateOrUpdate/Delete are plain calls, not Begin* pollers, so there is no LRO
// plumbing and no operationStatuses responder to wire.
package managedidentity

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.ManagedIdentity"
	resourceType = "userAssignedIdentities"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal managed-identity backend the handler needs.
// *managedidentity.Mock satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string, in managedidentity.Input) (managedidentity.Identity, bool, error)
	Get(ctx context.Context, sub, rg, name string) (managedidentity.Identity, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]managedidentity.Identity, error)
	ListBySubscription(ctx context.Context, sub string) ([]managedidentity.Identity, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.ManagedIdentity/userAssignedIdentities ARM requests.
type Handler struct {
	store Store
}

// New returns a managed-identity handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a userAssignedIdentities ARM URL. The
// provider and type are matched case-insensitively because SDK URL templates and
// hand-written tooling differ in casing.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, resourceType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A collection URL (no resource name) is a list — by resource group when the
	// path carried one, otherwise by subscription.
	if rp.ResourceName == "" {
		h.list(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodPatch:
		h.update(w, r, &rp)
	case http.MethodGet:
		h.get(w, r, &rp)
	case http.MethodDelete:
		h.delete(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// PurgeResourceGroup deletes every identity under sub/rg so a resource-group
// delete cascades into its identities (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req identityRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := managedidentity.Input{Location: req.Location, Tags: req.Tags}

	id, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&id))
}

// update applies an ARM PATCH: location and tags are replaced when supplied; the
// minted ids are preserved. A PATCH on a missing identity is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req identityRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := managedidentity.Input{Location: existing.Location, Tags: existing.Tags}
	if req.Location != "" {
		in.Location = req.Location
	}

	if req.Tags != nil {
		in.Tags = req.Tags
	}

	id, _, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&id))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	id, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&id))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing identity returns 204 No Content, a
	// deleted one returns 200 OK. The armmsi client accepts both.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		ids []managedidentity.Identity
		err error
	)

	if rp.ResourceGroup != "" {
		ids, err = h.store.ListByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		ids, err = h.store.ListBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]identityResponse, 0, len(ids))}
	for i := range ids {
		out.Value = append(out.Value, toResponse(&ids[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}
