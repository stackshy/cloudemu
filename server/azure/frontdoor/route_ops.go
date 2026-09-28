package frontdoor

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveRoute routes .../afdEndpoints/{ep}/routes[/{r}] (Routes.*). The handlers
// decode the body, translate the originGroup ARM id into a name in this profile,
// and map errors; validation, defaults and the conflict rule live in the
// provider.
func (h *Handler) serveRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	dispatchNested(w, r, np, &nestedOps{
		put: h.createOrUpdateRoute, get: h.getRoute, patch: h.updateRoute,
		del: h.deleteRoute, list: h.listRoutes,
	})
}

// createOrUpdateRoute handles PUT (Routes.BeginCreate): a full replace.
func (h *Handler) createOrUpdateRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	route, ok := decodeRoute(w, r, np)
	if !ok {
		return
	}

	stored, created, err := h.fd.CreateOrUpdateRoute(r.Context(), np.rg, np.profile, np.parent, np.name, route)
	if err != nil {
		writePutErr(w, err)
		return
	}

	writeCreated(w, created, toRouteJSON(np.sub, stored))
}

// updateRoute handles PATCH (Routes.BeginUpdate): the provider overlays the
// supplied property keys on the stored ones and re-validates the merge (so
// repointing originGroup at a missing group, or into a conflict, is refused).
func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	patch, ok := decodeRoute(w, r, np)
	if !ok {
		return
	}

	stored, err := h.fd.UpdateRoute(r.Context(), np.rg, np.profile, np.parent, np.name, patch)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRouteJSON(np.sub, stored))
}

// decodeRoute reads a route body into the driver shape, resolving
// properties.originGroup.id (when supplied) to an origin-group name. It writes the
// error response and returns false on failure.
func decodeRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) (fddriver.AzureFrontDoorRoute, bool) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return fddriver.AzureFrontDoorRoute{}, false
	}

	props := stripKeys(body.Properties, routeComputedKeys()...)

	originGroup, err := resolveRouteOriginGroup(np, props)
	if err != nil {
		writeErr(w, err)
		return fddriver.AzureFrontDoorRoute{}, false
	}

	return fddriver.AzureFrontDoorRoute{OriginGroup: originGroup, Properties: props}, true
}

func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.GetRoute(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRouteJSON(np.sub, stored))
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	writeDeleteResult(w, h.fd.DeleteRoute(r.Context(), np.rg, np.profile, np.parent, np.name))
}

//nolint:dupl // parallel to listOrigins over distinct grandchild types and driver methods.
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.ListRoutes(r.Context(), np.rg, np.profile, np.parent)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := nestedListResult{Value: make([]nestedJSON, 0, len(stored))}
	for i := range stored {
		out.Value = append(out.Value, toRouteJSON(np.sub, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// routeComputedKeys are the read-only route properties Azure computes.
func routeComputedKeys() []string {
	return []string{provisioningStateKey, deploymentStatusKey, endpointNameKey}
}

// resolveRouteOriginGroup translates properties.originGroup.id into the name of
// an origin group in the route's own profile. An absent originGroup yields ""
// (the provider requires it on create; a PATCH keeps the stored one). A
// malformed id, or one addressing another subscription, resource group or
// profile, is refused.
func resolveRouteOriginGroup(np *nestedPath, props map[string]any) (string, error) {
	raw, present := props[originGroupKey]
	if !present || raw == nil {
		return "", nil
	}

	ref, _ := raw.(map[string]any)
	id, _ := ref["id"].(string)

	if id == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "front door route properties.originGroup.id is required")
	}

	rp, ok := azurearm.ParsePath(id)
	if !ok || !isOriginGroupRef(&rp) {
		return "", cerrors.Newf(cerrors.InvalidArgument, "front door route originGroup id %q is not an origin group id", id)
	}

	if !strings.EqualFold(rp.Subscription, np.sub) || !strings.EqualFold(rp.ResourceGroup, np.rg) ||
		!strings.EqualFold(rp.ResourceName, np.profile) {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"front door route originGroup %q must belong to profile %q", id, np.profile)
	}

	return rp.SubResourceName, nil
}

// isOriginGroupRef reports whether rp is .../Microsoft.Cdn/profiles/{p}/originGroups/{og}.
func isOriginGroupRef(rp *azurearm.ResourcePath) bool {
	return strings.EqualFold(rp.Provider, providerName) && isProfilesType(rp.ResourceType) &&
		strings.EqualFold(rp.SubResource, subTypeOrigGroups) && rp.SubResourceName != "" &&
		rp.SubResourceAction == ""
}

// toRouteJSON reconstructs the ARM route body from the stored route (so the id,
// name and endpointName carry the stored casing), stamping the computed
// read-only properties.
func toRouteJSON(sub string, rt *fddriver.AzureFrontDoorRoute) nestedJSON {
	id := grandchildID(sub, rt.ResourceGroup, rt.Profile, subTypeEndpoints, rt.Endpoint, subTypeRoutes, rt.Name)

	return nestedJSON{
		ID:   id,
		Name: rt.Name,
		Type: routeResourceType,
		Etag: storedETag(rt.ETag, id),
		Properties: withComputed(rt.Properties, map[string]any{
			endpointNameKey:      rt.Endpoint,
			provisioningStateKey: provisioningStateSucceeded,
			deploymentStatusKey:  deploymentStatusNotStarted,
		}),
	}
}
