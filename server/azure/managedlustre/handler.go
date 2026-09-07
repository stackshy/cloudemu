// Package managedlustre serves the Azure Managed Lustre ARM API
// (Microsoft.StorageCache/amlFilesystems). Real armstoragecache
// AmlFilesystemsClient requests hit this handler the same way they hit
// management.azure.com.
//
// Real Azure runs CreateOrUpdate/Update/Delete and the archive actions as
// long-running operations; the emulator completes them synchronously
// (sync-200/201) with provisioningState=Succeeded, so there is no LRO plumbing
// to wire.
package managedlustre

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedlustre"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.StorageCache"
	resourceType = "amlFilesystems"
	armType      = providerName + "/" + resourceType
	// healthStateAvailable / healthDescriptionOK are the computed, stable health
	// values every ready amlFilesystem reports.
	healthStateAvailable = "Available"
	healthDescriptionOK  = "amlFilesystem is ok."
)

// Store is the minimal Managed Lustre backend the handler needs.
// *managedlustre.Mock satisfies it.
type Store interface {
	CreateOrUpdate(
		ctx context.Context, sub, rg, name, location string, in *managedlustre.Input,
	) (managedlustre.AmlFilesystem, bool, error)
	Get(ctx context.Context, sub, rg, name string) (managedlustre.AmlFilesystem, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	Archive(ctx context.Context, sub, rg, name, filesystemPath string) error
	CancelArchive(ctx context.Context, sub, rg, name string) error
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]managedlustre.AmlFilesystem, error)
	ListBySubscription(ctx context.Context, sub string) ([]managedlustre.AmlFilesystem, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.StorageCache/amlFilesystems ARM requests.
type Handler struct {
	store Store
}

// New returns a Managed Lustre handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets an amlFilesystems ARM URL. The provider and
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

	// POST sub-resource actions on a named resource (archive, cancelArchive).
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

// PurgeResourceGroup deletes every amlFilesystem under sub/rg so a
// resource-group delete cascades into its file systems.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveAction routes the POST sub-resource actions on a named amlFilesystem.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case "archive":
		h.archive(w, r, rp)
	case "cancelarchive":
		h.cancelArchive(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResource)
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req fsRequest
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
// stored resource; the immutable location and unmentioned fields are preserved.
// A PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req fsRequest
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

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a deleted resource returns 200 OK, a missing one
	// returns 204 No Content. The armstoragecache client accepts both.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// archive runs the POST /archive action, recording a completed archive status.
func (h *Handler) archive(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req archiveRequest
	if r.ContentLength != 0 && !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.store.Archive(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.FilesystemPath); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// cancelArchive runs the POST /cancelArchive action.
func (h *Handler) cancelArchive(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.store.CancelArchive(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []managedlustre.AmlFilesystem
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

	out := listResponse{Value: make([]fsResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}
