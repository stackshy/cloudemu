// Package frontdoor implements the Azure Front Door Standard/Premium
// (Microsoft.Cdn/profiles) ARM REST API: the profile plus its two
// independently-addressable child types, afdEndpoints and originGroups, as a
// server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn ProfilesClient /
// AFDEndpointsClient / AFDOriginGroupsClient clients, and the Terraform azurerm
// azurerm_cdn_frontdoor_* resources, configured with a custom endpoint hit this
// handler the same way they hit management.azure.com, driving the shared frontdoor
// driver.
//
// Microsoft.Cdn is a new ARM provider namespace, disjoint from every existing
// handler, so registration order is unconstrained; it registers before the
// permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../profiles/{p}                          : Profiles.BeginCreateOrUpdate (201/200)
//	GET    .../profiles/{p}                          : Profiles.Get
//	PATCH  .../profiles/{p}                          : Profiles.Update (tags REPLACE)
//	DELETE .../profiles/{p}                          : Profiles.BeginDelete (cascades children)
//	GET    .../{scope}/…/profiles                    : Profiles.List / ListByResourceGroup
//	PUT/GET/PATCH/DELETE .../profiles/{p}/afdEndpoints/{ep}   : AFDEndpoints.*
//	GET    .../profiles/{p}/afdEndpoints                       : AFDEndpoints.ListByProfile
//	PUT/GET/DELETE .../profiles/{p}/originGroups/{og}          : AFDOriginGroups.*
//	GET    .../profiles/{p}/originGroups                       : AFDOriginGroups.ListByProfile
//	PUT/GET/PATCH/DELETE .../originGroups/{og}/origins/{o}      : AFDOrigins.* (hostName required)
//	GET    .../originGroups/{og}/origins                      : AFDOrigins.ListByOriginGroup
//	PUT/GET/PATCH/DELETE .../afdEndpoints/{ep}/routes/{r}      : Routes.* (originGroup must exist in the profile)
//	GET    .../afdEndpoints/{ep}/routes                       : Routes.ListByEndpoint
//
// An origin group still referenced by a route cannot be deleted (409 Conflict).
// Deleting an origin group removes its origins; deleting an endpoint removes its
// routes; deleting a profile removes everything under it.
//
// The whole resource arrives in one PUT body and fully replaces the stored state
// (ARM CreateOrUpdate). The profile's sku, location, kind and identity are modeled
// explicitly (the server-wide echo only reaches the top-level properties object);
// the computed frontDoorId (a stable synthetic GUID) and the endpoint hostName (a
// stable computed <name>-<hash>.z01.azurefd.net) are derived deterministically so
// they never drift across GETs. Every other property is preserved verbatim so
// deferred sub-surfaces (ruleSets, securityPolicies, customDomains, secrets)
// stay echo-through-safe.
package frontdoor

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// Handler serves Microsoft.Cdn/profiles ARM requests (profile + afdEndpoints +
// originGroups) against a Front Door driver.
type Handler struct {
	fd fddriver.AzureFrontDoorProfiles
}

// New returns a Front Door handler backed by fd.
func New(fd fddriver.AzureFrontDoorProfiles) *Handler {
	return &Handler{fd: fd}
}

// isProfilesType reports whether the ARM resource type is profiles.
func isProfilesType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeProfiles)
}

// Matches claims ARM URLs targeting Microsoft.Cdn/profiles (and its afdEndpoints /
// originGroups children). Microsoft.Cdn is a new provider namespace, so
// registration order is unconstrained; registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isProfilesType(rp.ResourceType)
}

// ServeHTTP routes on the sub-resource segment, then path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A sub-resource segment addresses an independently-addressable child of the
	// profile (afdEndpoints / originGroups), a grandchild under one of those
	// (routes / origins), or a deferred deeper surface.
	switch {
	case rp.SubResource == "":
		h.serveProfile(w, r, &rp)
	case rp.SubResourceAction == "":
		h.serveChild(w, r, &rp)
	default:
		h.serveGrandchild(w, r, &rp)
	}
}

// serveChild routes .../profiles/{p}/{afdEndpoints|originGroups}[/{name}].
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch {
	case strings.EqualFold(rp.SubResource, subTypeEndpoints):
		h.serveEndpoint(w, r, rp)
	case strings.EqualFold(rp.SubResource, subTypeOrigGroups):
		h.serveOriginGroup(w, r, rp)
	default:
		writeSubResourceDeferred(w)
	}
}

// serveGrandchild routes .../afdEndpoints/{ep}/routes[/{r}] and
// .../originGroups/{og}/origins[/{o}]; anything else under a child is deferred.
func (h *Handler) serveGrandchild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	np, ok := parseNestedPath(r.URL.Path, rp)
	if !ok || np.parent == "" {
		writeSubResourceDeferred(w)
		return
	}

	switch {
	case strings.EqualFold(rp.SubResource, subTypeEndpoints) && strings.EqualFold(rp.SubResourceAction, subTypeRoutes):
		h.serveRoute(w, r, &np)
	case strings.EqualFold(rp.SubResource, subTypeOrigGroups) && strings.EqualFold(rp.SubResourceAction, subTypeOrigins):
		h.serveOrigin(w, r, &np)
	default:
		writeSubResourceDeferred(w)
	}
}

// serveProfile routes a profile request (no sub-resource segment).
//
//nolint:dupl // the profile and origin-group routers are parallel over distinct types and driver methods.
func (h *Handler) serveProfile(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listProfiles(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateProfile(w, r, rp)
	case http.MethodGet:
		h.getProfile(w, r, rp)
	case http.MethodPatch:
		h.updateProfileTags(w, r, rp)
	case http.MethodDelete:
		h.deleteProfile(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// PurgeResourceGroup deletes every Front Door profile (cascading its endpoints and
// origin groups) stored under the given resource group, backing the resource-group
// cascade delete. Best-effort: a single failure is returned but does not stop the
// remaining teardown. The subscription is unused (the emulator is single-estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	profiles, err := h.fd.ListProfiles(ctx, resourceGroup)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range profiles {
		if derr := h.fd.DeleteProfile(ctx, profiles[i].ResourceGroup, profiles[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// writeSubResourceDeferred rejects an addressable sub-resource path (ruleSets,
// securityPolicies, customDomains, secrets) that the core control plane
// does not model yet.
func writeSubResourceDeferred(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "NotFound",
		"azure front door sub-resource is not supported")
}
