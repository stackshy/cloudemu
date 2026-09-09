// Package purview serves the Microsoft Purview ARM API
// (Microsoft.Purview/accounts). Real armpurview AccountsClient requests hit this
// handler the same way they hit management.azure.com.
//
// Real Azure runs CreateOrUpdate/Update/Delete as long-running operations; the
// emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded, so there is no LRO plumbing to wire.
package purview

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/purview"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.Purview"
	resourceType = "accounts"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal Purview backend the handler needs.
// *purview.Mock satisfies it.
type Store interface {
	CreateOrUpdate(
		ctx context.Context, sub, rg, name, location string, in *purview.Input,
	) (purview.Account, bool, error)
	Get(ctx context.Context, sub, rg, name string) (purview.Account, error)
	ListKeys(ctx context.Context, sub, rg, name string) (purview.Keys, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]purview.Account, error)
	ListBySubscription(ctx context.Context, sub string) ([]purview.Account, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.Purview/accounts ARM requests.
type Handler struct {
	store Store
}

// New returns a Purview handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a Purview account ARM URL. The provider and
// type are matched case-insensitively because SDK URL templates and hand-written
// tooling differ in casing.
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

	// Sub-resource POST actions on a named resource (listKeys).
	if rp.SubResource != "" {
		h.serveAction(w, r, &rp)
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

// PurgeResourceGroup deletes every Purview account under sub/rg so a
// resource-group delete cascades into its accounts.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveAction routes the POST sub-resource actions on a named resource.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "listkeys":
		h.listKeys(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResource)
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req accountRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := inputFromRequest(&req)

	s, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&s))
}

// update applies an ARM PATCH: only the supplied fields are overlaid onto the
// stored resource; the computed ids, the immutable location and unmentioned
// fields are preserved. A PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req accountRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := inputFromRequest(&req)

	s, _, err := h.store.CreateOrUpdate(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&s))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&s))
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	keys, err := h.store.ListKeys(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, listKeysResponse{
		AtlasKafkaPrimaryEndpoint:   keys.AtlasKafkaPrimaryEndpoint,
		AtlasKafkaSecondaryEndpoint: keys.AtlasKafkaSecondaryEndpoint,
	})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armpurview client accepts both.
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
		items []purview.Account
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]accountResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}
