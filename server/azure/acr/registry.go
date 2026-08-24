package acr

import (
	"net/http"
	"strings"

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
	case http.MethodPut, http.MethodPatch:
		h.createOrUpdateRegistry(w, r, rp)
	case http.MethodGet:
		h.getRegistry(w, r, rp)
	case http.MethodDelete:
		h.deleteRegistry(w, r, rp)
	default:
		armMethodNotAllowed(w)
	}
}

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
	}

	if body.Properties != nil {
		cfg.AdminUserEnabled = body.Properties.AdminUserEnabled
	}

	reg, err := h.mgr.CreateOrUpdateRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName, cfg)
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
	if err := h.mgr.DeleteRegistry(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
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

func armMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
