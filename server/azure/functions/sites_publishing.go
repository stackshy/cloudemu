package functions

import (
	"encoding/json"
	"net/http"
	"strings"

	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	checkNameType       = "checknameavailability"
	publishingPolicyFTP = "ftp"
	publishingPolicySCM = "scm"
)

// publishingPolicy is the CsmPublishingCredentialsPoliciesEntity body.
type publishingPolicy struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Type       string                `json:"type"`
	Properties publishingPolicyProps `json:"properties"`
}

type publishingPolicyProps struct {
	Allow *bool `json:"allow"`
}

// servePublishingPolicies serves .../sites/{n}/basicPublishingCredentialsPolicies
// [/{ftp|scm}]: GET and PUT of the basic-auth allow flag, which defaults to true.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func servePublishingPolicies(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	kind := strings.ToLower(rp.SubResourceName)

	meta, err := store.GetSiteMeta(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	switch {
	case rp.SubResourceAction != "":
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown publishing policy route")
	case kind == "" && r.Method == http.MethodGet:
		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": []publishingPolicy{
			toPublishingPolicy(rp, meta, publishingPolicyFTP),
			toPublishingPolicy(rp, meta, publishingPolicySCM),
		}})
	case kind != publishingPolicyFTP && kind != publishingPolicySCM:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown publishing policy "+rp.SubResourceName)
	case r.Method == http.MethodGet:
		azurearm.WriteJSON(w, http.StatusOK, toPublishingPolicy(rp, meta, kind))
	case r.Method == http.MethodPut:
		putPublishingPolicy(w, r, rp, store, kind)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported publishing policy route")
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func putPublishingPolicy(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps, kind string,
) {
	var req publishingPolicy
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	if req.Properties.Allow == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", "properties.allow is required")
		return
	}

	meta, err := store.SetPublishingPolicy(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		kind, *req.Properties.Allow)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPublishingPolicy(rp, meta, kind))
}

//nolint:gocritic // rp is request-scoped.
func toPublishingPolicy(rp azurearm.ResourcePath, meta *azfunctions.SiteMeta, kind string) publishingPolicy {
	allow, ok := meta.PublishingPolicies[kind]
	if !ok {
		allow = true
	}

	return publishingPolicy{
		ID:         siteID(rp) + "/basicPublishingCredentialsPolicies/" + kind,
		Name:       kind,
		Type:       providerName + "/" + resourceType + "/basicPublishingCredentialsPolicies",
		Properties: publishingPolicyProps{Allow: &allow},
	}
}

// isCheckNameRequest reports a subscription-level
// POST /subscriptions/{s}/providers/Microsoft.Web/checknameavailability.
//
//nolint:gocritic // rp is request-scoped.
func isCheckNameRequest(rp azurearm.ResourcePath) bool {
	return strings.EqualFold(rp.Provider, providerName) && rp.ResourceGroup == "" &&
		strings.EqualFold(rp.ResourceType, checkNameType) && rp.ResourceName == ""
}

// checkNameRequest is the ResourceNameAvailabilityRequest body.
type checkNameRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// checkNameResponse is the ResourceNameAvailability body.
type checkNameResponse struct {
	NameAvailable bool   `json:"nameAvailable"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
}

// serveCheckName answers Microsoft.Web checknameavailability. Site names are
// global, so a site in any resource group makes the name unavailable. Other
// resource types are not tracked here and report available.
func (h *Handler) serveCheckName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "checknameavailability requires POST")
		return
	}

	var req checkNameRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", "name is required")
		return
	}

	isSite := strings.EqualFold(req.Type, "Site") || strings.EqualFold(req.Type, providerName+"/"+resourceType)

	store, ok := h.siteStore()
	if !ok || !isSite || !store.IsSiteNameTaken(r.Context(), req.Name) {
		azurearm.WriteJSON(w, http.StatusOK, checkNameResponse{NameAvailable: true})
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, checkNameResponse{
		Reason:  "AlreadyExists",
		Message: "Hostname '" + req.Name + "' already exists. Please select a different name.",
	})
}

// rawSiteConfig extracts properties.siteConfig from a site PUT body, minus
// the app settings and connection strings, which are stored on their own.
func rawSiteConfig(body []byte) json.RawMessage {
	var req struct {
		Properties struct {
			SiteConfig map[string]any `json:"siteConfig"`
		} `json:"properties"`
	}

	if json.Unmarshal(body, &req) != nil || req.Properties.SiteConfig == nil {
		return nil
	}

	if cfg := sanitizeSiteConfig(req.Properties.SiteConfig); cfg != nil {
		return cfg
	}

	return json.RawMessage(emptyObject)
}
