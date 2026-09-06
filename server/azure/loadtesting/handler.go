// Package loadtesting serves the Azure Load Testing ARM API
// (Microsoft.LoadTestService/loadTests). Real armloadtesting LoadTestsClient
// requests hit this handler the same way they hit management.azure.com.
//
// Every operation is synchronous (sync-200/201): CreateOrUpdate and Delete
// complete in-line, so there is no long-running-operation plumbing to wire.
package loadtesting

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/loadtesting"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.LoadTestService"
	resourceType = "loadTests"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal load-testing backend the handler needs.
// *loadtesting.Mock satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string, in loadtesting.Input) (loadtesting.LoadTest, bool, error)
	Get(ctx context.Context, sub, rg, name string) (loadtesting.LoadTest, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]loadtesting.LoadTest, error)
	ListBySubscription(ctx context.Context, sub string) ([]loadtesting.LoadTest, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.LoadTestService/loadTests ARM requests.
type Handler struct {
	store Store
}

// New returns a load-testing handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a loadTests ARM URL. The provider and type
// are matched case-insensitively because SDK URL templates and hand-written
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

// PurgeResourceGroup deletes every load test under sub/rg so a resource-group
// delete cascades into its load tests (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req loadTestRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := loadtesting.Input{
		Location:   req.Location,
		Tags:       req.Tags,
		Identity:   toDriverIdentity(req.Identity),
		Encryption: encryptionOf(req.Properties),
	}
	if desc := descriptionOf(req.Properties); desc != nil {
		in.Description = *desc
	}

	lt, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&lt))
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

	var req loadTestRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := mergePatch(&existing, &req)

	lt, _, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&lt))
}

// mergePatch overlays the fields present in req onto the stored resource,
// producing the Input for a create-or-update that preserves everything the PATCH
// did not mention. Location is immutable and always taken from the stored value.
func mergePatch(existing *loadtesting.LoadTest, req *loadTestRequest) loadtesting.Input {
	in := loadtesting.Input{
		Location:    existing.Location,
		Tags:        existing.Tags,
		Description: existing.Description,
		Identity:    identityInputFrom(existing.Identity),
		Encryption:  existing.Encryption,
	}

	if req.Tags != nil {
		in.Tags = req.Tags
	}

	if req.Identity != nil {
		in.Identity = toDriverIdentity(req.Identity)
	}

	if desc := descriptionOf(req.Properties); desc != nil {
		in.Description = *desc
	}

	if enc := encryptionOf(req.Properties); enc != nil {
		in.Encryption = enc
	}

	return in
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	lt, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&lt))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armloadtesting client accepts both.
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
		items []loadtesting.LoadTest
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

	out := listResponse{Value: make([]loadTestResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// descriptionOf returns the description pointer from a properties block, or nil.
func descriptionOf(p *propertiesRequest) *string {
	if p == nil {
		return nil
	}

	return p.Description
}

// encryptionOf returns the driver encryption from a properties block, or nil.
func encryptionOf(p *propertiesRequest) *loadtesting.Encryption {
	if p == nil {
		return nil
	}

	return toDriverEncryption(p.Encryption)
}

// identityInputFrom rebuilds a create/update identity input from a stored
// identity, so a PATCH that omits identity preserves it. Only the type and the
// user-assigned id keys are carried; the mock re-mints the ids deterministically.
func identityInputFrom(in *loadtesting.Identity) *loadtesting.Identity {
	if in == nil {
		return nil
	}

	out := &loadtesting.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]loadtesting.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = loadtesting.UserAssignedValue{}
		}
	}

	return out
}
