package acr

import (
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// ARMHandler serves the Microsoft.ContainerRegistry ARM management plane
// (registries, admin credentials, usages, webhooks, geo-replications) against
// an AzureRegistryManager. It is registered alongside the /acr/v1 data-plane
// Handler; the two match disjoint path prefixes.
type ARMHandler struct {
	mgr crdriver.AzureRegistryManager
}

// NewARM returns an ARM handler backed by mgr.
func NewARM(mgr crdriver.AzureRegistryManager) *ARMHandler {
	return &ARMHandler{mgr: mgr}
}

// Matches claims ARM Microsoft.ContainerRegistry/registries paths.
func (*ARMHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, armProviderName) &&
		strings.EqualFold(rp.ResourceType, resourceTypeRegistries)
}

// ServeHTTP routes on path shape and method.
func (h *ARMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.serveRegistryCollection(w, r, &rp)
		return
	}

	switch {
	case rp.SubResource == "":
		h.serveRegistry(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "listCredentials"):
		h.postListCredentials(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "regenerateCredential"):
		h.postRegenerateCredential(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "listUsages"):
		h.getListUsages(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "webhooks"):
		h.serveWebhook(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "replications"):
		h.serveReplication(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"ACR sub-resource not implemented: "+rp.SubResource)
	}
}

func (h *ARMHandler) serveRegistryCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		armMethodNotAllowed(w)
		return
	}

	regs, err := h.mgr.ListRegistries(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armRegistry, 0, len(regs))
	for i := range regs {
		out = append(out, toARMRegistry(&regs[i], rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armRegistryList[armRegistry]{Value: out})
}

func (h *ARMHandler) serveRegistry(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateRegistry(w, r, rp)
	case http.MethodPatch:
		h.updateRegistry(w, r, rp)
	case http.MethodGet:
		h.getRegistry(w, r, rp)
	case http.MethodDelete:
		h.deleteRegistry(w, r, rp)
	default:
		armMethodNotAllowed(w)
	}
}

// createOrUpdateRegistry handles the ARM PUT (full create-or-replace): every
// attribute is taken from the request body, replacing the stored registry.
func (h *ARMHandler) createOrUpdateRegistry(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armRegistry
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := crdriver.AzureRegistryConfig{
		Location: body.Location,
		Tags:     fromPtrTags(body.Tags),
	}

	if body.SKU != nil {
		cfg.SKUName = body.SKU.Name
	}

	if body.Identity != nil {
		cfg.IdentityType = body.Identity.Type
		cfg.UserAssignedIdentities = identityKeys(body.Identity.UserAssignedIdentities)
	}

	if body.Properties != nil {
		cfg.AdminUserEnabled = body.Properties.AdminUserEnabled
	}

	reg, created, err := h.mgr.CreateOrUpdateRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName, cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, createdStatus(created), toARMRegistry(reg, rp.Subscription))
}

// updateRegistry handles the ARM PATCH (partial update): only attributes
// present in the request body are overwritten; the rest are preserved.
func (h *ARMHandler) updateRegistry(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armRegistryUpdateParameters
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var upd crdriver.AzureRegistryUpdate

	if body.Tags != nil {
		upd.Tags = fromPtrTags(body.Tags)
	}

	if body.SKU != nil {
		sku := body.SKU.Name
		upd.SKUName = &sku
	}

	if body.Identity != nil {
		id := body.Identity.Type
		upd.IdentityType = &id
		upd.UserAssignedIdentities = identityKeys(body.Identity.UserAssignedIdentities)
	}

	if body.Properties != nil && body.Properties.AdminUserEnabled != nil {
		upd.AdminUserEnabled = body.Properties.AdminUserEnabled
	}

	reg, err := h.mgr.UpdateRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName, upd)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMRegistry(reg, rp.Subscription))
}

func (h *ARMHandler) getRegistry(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	reg, err := h.mgr.GetRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMRegistry(reg, rp.Subscription))
}

func (h *ARMHandler) deleteRegistry(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	writeDeleteStatus(w, h.mgr.DeleteRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName))
}

// writeDeleteStatus renders an idempotent ARM DELETE result: 200 OK when the
// resource existed and was removed, 204 No Content when it was already absent.
// ARM DELETE is idempotent — the ACR swagger documents 204 "does not exist in
// the subscription" for a missing registry/webhook/replication.
func writeDeleteStatus(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case cerrors.IsNotFound(err):
		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteCErr(w, err)
	}
}

func (h *ARMHandler) postListCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		armMethodNotAllowed(w)
		return
	}

	creds, err := h.mgr.ListRegistryCredentials(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toCredentialsResult(creds))
}

func (h *ARMHandler) postRegenerateCredential(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		armMethodNotAllowed(w)
		return
	}

	var body armRegenerateCredentialParameters
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	creds, err := h.mgr.RegenerateRegistryCredential(r.Context(), rp.ResourceGroup, rp.ResourceName, body.Name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toCredentialsResult(creds))
}

func toCredentialsResult(creds *crdriver.AzureRegistryCredentials) armRegistryListCredentialsResult {
	return armRegistryListCredentialsResult{
		Username: creds.Username,
		Passwords: []armRegistryPassword{
			{Name: "password", Value: creds.Password},
			{Name: "password2", Value: creds.Password2},
		},
	}
}

func (h *ARMHandler) getListUsages(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		armMethodNotAllowed(w)
		return
	}

	usages, err := h.mgr.ListRegistryUsages(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armRegistryUsage, 0, len(usages))
	for i := range usages {
		out = append(out, armRegistryUsage{
			Name:         usages[i].Name,
			Limit:        usages[i].Limit,
			CurrentValue: usages[i].CurrentValue,
			Unit:         usages[i].Unit,
		})
	}

	azurearm.WriteJSON(w, http.StatusOK, armRegistryUsageListResult{Value: out})
}

// identityKeys returns the user-assigned identity resource IDs (the map keys) in
// deterministic order so the synthesized principal/client pairs are stable.
func identityKeys(m map[string]*armUserAssignedIdentity) []string {
	if len(m) == 0 {
		return nil
	}

	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func armMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// createdStatus maps an ARM PUT create-or-replace outcome to its status code:
// 201 Created on first create, 200 OK on replace.
func createdStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}
