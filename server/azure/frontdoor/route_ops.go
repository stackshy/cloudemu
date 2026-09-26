package frontdoor

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fddriver "github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// serveRoute routes .../afdEndpoints/{ep}/routes[/{r}] (Routes.*).
func (h *Handler) serveRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	dispatchNested(w, r, np, &nestedOps{
		put: h.createOrUpdateRoute, get: h.getRoute, patch: h.updateRoute,
		del: h.deleteRoute, list: h.listRoutes,
	})
}

// createOrUpdateRoute handles PUT (Routes.BeginCreate): a full replace.
func (h *Handler) createOrUpdateRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	h.storeRoute(w, r, np, stripKeys(body.Properties, routeComputedKeys()...))
}

// updateRoute handles PATCH (Routes.BeginUpdate): supplied property keys overlay
// the stored ones, and the merged route is re-validated (so repointing
// originGroup at a missing group is refused).
//
//nolint:dupl // parallel to updateOrigin over distinct grandchild types and driver methods.
func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	var body nestedJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fd.GetRoute(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	patch := stripKeys(body.Properties, routeComputedKeys()...)
	h.storeRoute(w, r, np, overlayProps(stored.Properties, patch))
}

// storeRoute validates props, resolves the origin-group reference and writes the
// route, answering 201 on create and 200 on replace.
func (h *Handler) storeRoute(w http.ResponseWriter, r *http.Request, np *nestedPath, props map[string]any) {
	originGroup, err := resolveRouteOriginGroup(np, props)
	if err == nil {
		err = validateRoute(props)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	stored, created, err := h.fd.CreateOrUpdateRoute(r.Context(), np.rg, np.profile, np.parent, np.name,
		fddriver.AzureFrontDoorRoute{OriginGroup: originGroup, Properties: props})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeCreated(w, created, toRouteJSON(np, stored))
}

func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.GetRoute(r.Context(), np.rg, np.profile, np.parent, np.name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRouteJSON(np, stored))
}

func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	if err := h.fd.DeleteRoute(r.Context(), np.rg, np.profile, np.parent, np.name); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // parallel to listOrigins over distinct grandchild types and driver methods.
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request, np *nestedPath) {
	stored, err := h.fd.ListRoutes(r.Context(), np.rg, np.profile, np.parent)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := nestedListResult{Value: make([]nestedJSON, 0, len(stored))}
	for i := range stored {
		out.Value = append(out.Value, toRouteJSON(np.withName(stored[i].Name), &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// routeComputedKeys are the read-only route properties Azure computes.
func routeComputedKeys() []string {
	return []string{provisioningStateKey, deploymentStatusKey, endpointNameKey}
}

// resolveRouteOriginGroup reads properties.originGroup.id and returns the origin
// group name. Azure requires the reference, and it must address an origin group
// in the route's own profile.
func resolveRouteOriginGroup(np *nestedPath, props map[string]any) (string, error) {
	ref, _ := props[originGroupKey].(map[string]any)
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

// validateRoute checks the route enums and patterns Azure validates, each only
// when supplied.
func validateRoute(props map[string]any) error {
	enums := []struct {
		key     string
		allowed []string
	}{
		{forwardingProtocolKey, []string{"HttpOnly", "HttpsOnly", "MatchRequest"}},
		{httpsRedirectKey, []string{stateEnabled, stateDisabled}},
		{linkToDefaultDomainKey, []string{stateEnabled, stateDisabled}},
		{enabledStateKey, []string{stateEnabled, stateDisabled}},
	}

	for _, e := range enums {
		if err := checkEnum(props, e.key, e.allowed); err != nil {
			return err
		}
	}

	if err := checkProtocols(props); err != nil {
		return err
	}

	return checkPatterns(props)
}

// checkEnum rejects a string property that is not one of allowed. An absent key
// passes.
func checkEnum(props map[string]any, key string, allowed []string) error {
	v, ok := props[key]
	if !ok || v == nil {
		return nil
	}

	if s, isStr := v.(string); isStr && containsFold(allowed, s) {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"front door route properties.%s must be one of %s", key, strings.Join(allowed, ", "))
}

// checkProtocols validates supportedProtocols: a list of Http / Https.
func checkProtocols(props map[string]any) error {
	allowed := []string{protocolHTTP, protocolHTTPS}

	return eachString(props, supportedProtocolsKey, func(s string) bool { return containsFold(allowed, s) },
		"front door route properties.supportedProtocols entries must be Http or Https")
}

// checkPatterns validates patternsToMatch: every pattern must start with "/".
func checkPatterns(props map[string]any) error {
	return eachString(props, patternsToMatchKey, func(s string) bool { return strings.HasPrefix(s, "/") },
		"front door route properties.patternsToMatch entries must start with /")
}

// eachString requires props[key], when present, to be a list of strings that
// all satisfy ok.
func eachString(props map[string]any, key string, ok func(string) bool, msg string) error {
	v, present := props[key]
	if !present || v == nil {
		return nil
	}

	list, isList := v.([]any)
	if !isList {
		return cerrors.New(cerrors.InvalidArgument, msg)
	}

	for _, item := range list {
		if s, isStr := item.(string); !isStr || !ok(s) {
			return cerrors.New(cerrors.InvalidArgument, msg)
		}
	}

	return nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}

	return false
}

// toRouteJSON reconstructs the ARM route body, stamping the id/etag, the
// defaults Azure reports for omitted fields, and the computed stamps.
func toRouteJSON(np *nestedPath, rt *fddriver.AzureFrontDoorRoute) nestedJSON {
	id := np.id(subTypeEndpoints, subTypeRoutes, np.name)

	const injected = 8

	props := copyProps(rt.Properties, injected)
	setDefault(props, enabledStateKey, enabledStateDefault)
	setDefault(props, forwardingProtocolKey, "MatchRequest")
	setDefault(props, httpsRedirectKey, stateDisabled)
	setDefault(props, linkToDefaultDomainKey, stateDisabled)
	setDefault(props, supportedProtocolsKey, []any{protocolHTTP, protocolHTTPS})

	props[endpointNameKey] = np.parent
	props[provisioningStateKey] = provisioningStateSucceeded
	props[deploymentStatusKey] = deploymentStatusNotStarted

	return nestedJSON{
		ID:         id,
		Name:       np.name,
		Type:       routeResourceType,
		Etag:       azurearm.WeakETag(id),
		Properties: props,
	}
}
