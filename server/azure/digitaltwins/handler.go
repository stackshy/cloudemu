// Package digitaltwins serves the Azure Digital Twins ARM API
// (Microsoft.DigitalTwins/digitalTwinsInstances). Real armdigitaltwins
// DigitalTwinsClient requests hit this handler the same way they hit
// management.azure.com.
//
// Every operation is synchronous (sync-200/201): CreateOrUpdate and Delete
// complete in-line, so there is no long-running-operation plumbing to wire.
package digitaltwins

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/digitaltwins"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.DigitalTwins"
	resourceType = "digitalTwinsInstances"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal Digital Twins backend the handler needs.
// *digitaltwins.Mock satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string, in digitaltwins.Input) (digitaltwins.Instance, bool, error)
	Get(ctx context.Context, sub, rg, name string) (digitaltwins.Instance, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]digitaltwins.Instance, error)
	ListBySubscription(ctx context.Context, sub string) ([]digitaltwins.Instance, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.DigitalTwins/digitalTwinsInstances ARM requests.
type Handler struct {
	store Store
}

// New returns a Digital Twins handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a digitalTwinsInstances ARM URL. The
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

// PurgeResourceGroup deletes every Digital Twins instance under sub/rg so a
// resource-group delete cascades into its instances
// (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req instanceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := digitaltwins.Input{
		Location:            req.Location,
		Tags:                req.Tags,
		Identity:            toDriverIdentity(req.Identity),
		PublicNetworkAccess: publicNetworkAccessOf(req.Properties),
	}

	inst, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&inst))
}

// update applies an ARM PATCH: only the supplied fields are overlaid onto the
// stored resource; the computed ids and unmentioned fields are preserved. A
// PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req instanceRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := mergePatch(&existing, &req)

	inst, _, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&inst))
}

// mergePatch overlays the fields present in req onto the stored resource,
// producing the Input for a create-or-update that preserves everything the PATCH
// did not mention. Location is immutable and always taken from the stored value.
func mergePatch(existing *digitaltwins.Instance, req *instanceRequest) digitaltwins.Input {
	in := digitaltwins.Input{
		Location:            existing.Location,
		Tags:                existing.Tags,
		Identity:            identityInputFrom(existing.Identity),
		PublicNetworkAccess: existing.PublicNetworkAccess,
	}

	if req.Tags != nil {
		in.Tags = req.Tags
	}

	if req.Identity != nil {
		in.Identity = toDriverIdentity(req.Identity)
	}

	if pna := publicNetworkAccessOf(req.Properties); pna != "" {
		in.PublicNetworkAccess = pna
	}

	return in
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	inst, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&inst))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armdigitaltwins client accepts both.
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
		items []digitaltwins.Instance
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

	out := listResponse{Value: make([]instanceResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// publicNetworkAccessOf returns the publicNetworkAccess value from a properties
// block, or "" when absent.
func publicNetworkAccessOf(p *propertiesRequest) string {
	if p == nil {
		return ""
	}

	return p.PublicNetworkAccess
}

// identityInputFrom rebuilds a create/update identity input from a stored
// identity, so a PATCH that omits identity preserves it. Only the type and the
// user-assigned id keys are carried; the mock re-mints the ids deterministically.
func identityInputFrom(in *digitaltwins.Identity) *digitaltwins.Identity {
	if in == nil {
		return nil
	}

	out := &digitaltwins.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]digitaltwins.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = digitaltwins.UserAssignedValue{}
		}
	}

	return out
}
