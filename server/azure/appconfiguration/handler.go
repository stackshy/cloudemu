// Package appconfiguration serves the Azure App Configuration ARM API
// (Microsoft.AppConfiguration/configurationStores). Real armappconfiguration
// ConfigurationStoresClient requests hit this handler the same way they hit
// management.azure.com.
//
// Every operation is synchronous (sync-200/201): CreateOrUpdate, Delete and the
// listKeys / regenerateKey actions complete in-line, so there is no
// long-running-operation plumbing to wire. The configuration data plane (the
// *.azconfig.io key-value store) is out of scope — only the store resource is
// emulated.
package appconfiguration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/appconfiguration"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.AppConfiguration"
	resourceType = "configurationStores"
	armType      = providerName + "/" + resourceType
)

// Store is the minimal configuration-store backend the handler needs.
// *appconfiguration.Mock satisfies it.
type Store interface {
	CreateOrUpdate(
		ctx context.Context, sub, rg, name string, in *appconfiguration.Input,
	) (appconfiguration.ConfigurationStore, bool, error)
	Get(ctx context.Context, sub, rg, name string) (appconfiguration.ConfigurationStore, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]appconfiguration.ConfigurationStore, error)
	ListBySubscription(ctx context.Context, sub string) ([]appconfiguration.ConfigurationStore, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.AppConfiguration/configurationStores ARM requests.
type Handler struct {
	store Store
}

// New returns a configuration-store handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a configuration-store ARM URL. The provider
// and type are matched case-insensitively because SDK URL templates and
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

	// Sub-resource POST actions on a named resource (listKeys, regenerateKeys).
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

// PurgeResourceGroup deletes every configuration store under sub/rg so a
// resource-group delete cascades into its stores.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveAction routes the POST sub-resource actions on a named store.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch rp.SubResource {
	case "listKeys":
		h.listKeys(w, r, rp)
	case "regenerateKey":
		h.regenerateKey(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResource)
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req configStoreRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := inputFromRequest(&req)

	s, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
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
// stored resource; the computed ids and unmentioned fields are preserved. A
// PATCH on a missing resource is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req configStoreRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := mergePatch(&existing, &req)

	s, _, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
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

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armappconfiguration client accepts both.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	s, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toKeysResponse(&s))
}

// regenerateKey returns the single ApiKey named by the request body's "id"
// field. The emulator's keys are deterministic and stable, so the returned key
// value is unchanged; only the ARM wire shape (a single ApiKey, not the listKeys
// envelope) and the id selection matter for client compatibility.
func (h *Handler) regenerateKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		ID string `json:"id"`
	}

	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	s, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	for i := range s.Keys {
		if s.Keys[i].Name == body.ID {
			azurearm.WriteJSON(w, http.StatusOK, toKeyWire(&s, &s.Keys[i]))
			return
		}
	}

	azurearm.WriteError(w, http.StatusNotFound, "KeyNotFound", "access key "+body.ID+" not found")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []appconfiguration.ConfigurationStore
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

	out := listResponse{Value: make([]configStoreResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// inputFromRequest builds a full create/update Input from a request body.
func inputFromRequest(req *configStoreRequest) appconfiguration.Input {
	in := appconfiguration.Input{
		Location: req.Location,
		Tags:     req.Tags,
		Sku:      toDriverSku(req.Sku),
		Identity: toDriverIdentity(req.Identity),
	}

	applyProperties(&in, req.Properties)

	return in
}

// applyProperties overlays a properties block onto in.
func applyProperties(in *appconfiguration.Input, p *propertiesRequest) {
	if p == nil {
		return
	}

	in.DisableLocalAuth = p.DisableLocalAuth
	in.EnablePurgeProtection = p.EnablePurgeProtection
	in.PublicNetworkAccess = p.PublicNetworkAccess
	in.SoftDeleteRetentionInDays = p.SoftDeleteRetentionInDays
	in.Encryption = toDriverEncryption(p.Encryption)
	in.DataPlaneProxy = toDriverDataPlaneProxy(p.DataPlaneProxy)
}

// mergePatch overlays the fields present in req onto the stored resource,
// producing the Input for a create-or-update that preserves everything the PATCH
// did not mention. Location is immutable and always taken from the stored value.
func mergePatch(existing *appconfiguration.ConfigurationStore, req *configStoreRequest) appconfiguration.Input {
	in := inputFromExisting(existing)

	if req.Tags != nil {
		in.Tags = req.Tags
	}

	if req.Sku != nil {
		in.Sku = toDriverSku(req.Sku)
	}

	if req.Identity != nil {
		in.Identity = toDriverIdentity(req.Identity)
	}

	mergeProperties(&in, req.Properties)

	return in
}

// mergeProperties overlays the properties present in p onto in.
func mergeProperties(in *appconfiguration.Input, p *propertiesRequest) {
	if p == nil {
		return
	}

	if p.DisableLocalAuth != nil {
		in.DisableLocalAuth = p.DisableLocalAuth
	}

	if p.EnablePurgeProtection != nil {
		in.EnablePurgeProtection = p.EnablePurgeProtection
	}

	if p.PublicNetworkAccess != "" {
		in.PublicNetworkAccess = p.PublicNetworkAccess
	}

	if p.SoftDeleteRetentionInDays != nil {
		in.SoftDeleteRetentionInDays = p.SoftDeleteRetentionInDays
	}

	if p.Encryption != nil {
		in.Encryption = toDriverEncryption(p.Encryption)
	}

	if p.DataPlaneProxy != nil {
		in.DataPlaneProxy = toDriverDataPlaneProxy(p.DataPlaneProxy)
	}
}

// inputFromExisting rebuilds a create/update input from a stored resource so a
// PATCH that omits a field preserves it. The identity type and user-assigned id
// keys are carried; the mock re-mints the ids deterministically.
func inputFromExisting(s *appconfiguration.ConfigurationStore) appconfiguration.Input {
	return appconfiguration.Input{
		Location:                  s.Location,
		Tags:                      s.Tags,
		Sku:                       s.Sku,
		Identity:                  identityInputFrom(s.Identity),
		DisableLocalAuth:          s.DisableLocalAuth,
		EnablePurgeProtection:     s.EnablePurgeProtection,
		PublicNetworkAccess:       s.PublicNetworkAccess,
		SoftDeleteRetentionInDays: s.SoftDeleteRetentionInDays,
		Encryption:                s.Encryption,
		DataPlaneProxy:            s.DataPlaneProxy,
	}
}

// identityInputFrom rebuilds a create/update identity input from a stored
// identity. Only the type and the user-assigned id keys are carried.
func identityInputFrom(in *appconfiguration.Identity) *appconfiguration.Identity {
	if in == nil {
		return nil
	}

	out := &appconfiguration.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]appconfiguration.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = appconfiguration.UserAssignedValue{}
		}
	}

	return out
}
