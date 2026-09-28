package frontdoor

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

// Origin and route property keys, enum values, defaults and ranges, taken from
// the Microsoft.Cdn 2024-02-01 schema (Microsoft.Cdn/stable/2024-02-01/afdx.json:
// AFDOriginProperties, AFDOriginUpdatePropertiesParameters, RouteProperties,
// RouteUpdatePropertiesParameters).
const (
	hostNameKey             = "hostName"
	httpPortKey             = "httpPort"
	httpsPortKey            = "httpsPort"
	priorityKey             = "priority"
	weightKey               = "weight"
	enabledStateKey         = "enabledState"
	enforceCertNameCheckKey = "enforceCertificateNameCheck"

	forwardingProtocolKey  = "forwardingProtocol"
	httpsRedirectKey       = "httpsRedirect"
	linkToDefaultDomainKey = "linkToDefaultDomain"
	supportedProtocolsKey  = "supportedProtocols"
	patternsToMatchKey     = "patternsToMatch"
	customDomainsKey       = "customDomains"
	ruleSetsKey            = "ruleSets"

	stateEnabled  = "Enabled"
	stateDisabled = "Disabled"
	protocolHTTP  = "Http"
	protocolHTTPS = "Https"

	forwardMatchRequest = "MatchRequest"

	// httpPort.default and httpsPort.default.
	defaultHTTPPort  = 80
	defaultHTTPSPort = 443

	// httpPort/httpsPort 1-65535, priority 1-5, weight 1-1000.
	minPort     = 1
	maxPort     = 65535
	minPriority = 1
	maxPriority = 5
	minWeight   = 1
	maxWeight   = 1000
)

// Azure's messages for the two dependency refusals, reproduced so a client
// matching on them behaves the same against cloudemu.
const (
	// hashicorp/terraform-provider-azurerm#27652 (AFDOriginsClient#Delete: StatusCode=400).
	lastOriginMsg = "Cannot disable or delete the last origin when the origin group is still associated with " +
		"a route or a rule. Please disassociate the origin group and try again."
	// Azure/bicep#13502.
	routeConflictMsg = "The route domains, paths and protocols configuration has a conflict."
)

// grandchildKey keys the origin and route stores by
// (resourceGroup, profile, parent, name), where parent is the origin group (for
// an origin) or the endpoint (for a route).
func grandchildKey(rg, profile, parent, name string) string {
	return childKey(rg, profile, parent) + "/" + strings.ToLower(name)
}

// CreateOrUpdateOrigin validates o, applies the schema defaults and stores it
// under (rg, profile, originGroup, name) as a full replace. The parent origin
// group must exist.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) CreateOrUpdateOrigin(
	_ context.Context, rg, profile, originGroup, name string, o driver.AzureFrontDoorOrigin,
) (*driver.AzureFrontDoorOrigin, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door origin name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	group, ok := m.originGroups.Get(childKey(rg, profile, originGroup))
	if !ok {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", originGroup)
	}

	existing, existed := m.origins.Get(grandchildKey(rg, profile, originGroup, name))

	var prev *driver.AzureFrontDoorOrigin
	if existed {
		prev = &existing
	}

	out, err := m.writeOriginLocked(&group, prev, name, cloneAnyMap(o.Properties))
	if err != nil {
		return nil, false, err
	}

	return out, !existed, nil
}

// UpdateOrigin overlays the supplied top-level properties on the stored origin
// (ARM PATCH) and re-validates the merged result, all under one lock.
func (m *Mock) UpdateOrigin(
	_ context.Context, rg, profile, originGroup, name string, patch map[string]any,
) (*driver.AzureFrontDoorOrigin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.origins.Get(grandchildKey(rg, profile, originGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin %q not found", name)
	}

	group, ok := m.originGroups.Get(childKey(rg, profile, originGroup))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", originGroup)
	}

	return m.writeOriginLocked(&group, &existing, name, overlay(existing.Properties, patch))
}

// writeOriginLocked validates and defaults props, refuses disabling the last
// enabled origin of a routed group, and stores the origin. prev is the stored
// origin being replaced, or nil on create. Callers hold m.mu.
func (m *Mock) writeOriginLocked(
	group *driver.AzureFrontDoorOriginGroup, prev *driver.AzureFrontDoorOrigin, name string, props map[string]any,
) (*driver.AzureFrontDoorOrigin, error) {
	if props == nil {
		props = map[string]any{}
	}

	if err := validateOrigin(props); err != nil {
		return nil, err
	}

	applyOriginDefaults(props)

	if prev != nil && originEnabled(prev.Properties) && !originEnabled(props) {
		if err := m.checkNotLastEnabledOrigin(group, name); err != nil {
			return nil, err
		}
	}

	stored := driver.AzureFrontDoorOrigin{
		Name:          name,
		ResourceGroup: group.ResourceGroup,
		Profile:       group.Profile,
		OriginGroup:   group.Name,
		Properties:    props,
		ETag:          idgen.UUID(),
	}

	// ARM keeps the casing a resource was created with.
	if prev != nil {
		stored.Name = prev.Name
	}

	m.origins.Set(grandchildKey(group.ResourceGroup, group.Profile, group.Name, name), stored)

	out := cloneOrigin(stored)

	return &out, nil
}

// GetOrigin returns the stored origin.
func (m *Mock) GetOrigin(_ context.Context, rg, profile, originGroup, name string) (*driver.AzureFrontDoorOrigin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.origins.Get(grandchildKey(rg, profile, originGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin %q not found", name)
	}

	out := cloneOrigin(o)

	return &out, nil
}

// DeleteOrigin removes the stored origin. Like Azure, it refuses
// (FailedPrecondition) to remove the last enabled origin of a group a route
// still forwards to.
func (m *Mock) DeleteOrigin(_ context.Context, rg, profile, originGroup, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := grandchildKey(rg, profile, originGroup, name)

	o, ok := m.origins.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "front door origin %q not found", name)
	}

	if originEnabled(o.Properties) {
		if group, found := m.originGroups.Get(childKey(rg, profile, originGroup)); found {
			if err := m.checkNotLastEnabledOrigin(&group, name); err != nil {
				return err
			}
		}
	}

	m.origins.Delete(key)

	return nil
}

// checkNotLastEnabledOrigin returns FailedPrecondition when group is referenced
// by a route and no enabled origin other than name would remain in it. Callers
// hold m.mu.
func (m *Mock) checkNotLastEnabledOrigin(group *driver.AzureFrontDoorOriginGroup, name string) error {
	route := m.routeReferencing(group.ResourceGroup, group.Profile, group.Name)
	if route == "" {
		return nil
	}

	for _, o := range m.origins.SortedValues() {
		if sameParent(o.ResourceGroup, o.Profile, o.OriginGroup, group.ResourceGroup, group.Profile, group.Name) &&
			!strings.EqualFold(o.Name, name) && originEnabled(o.Properties) {
			return nil
		}
	}

	return cerrors.Newf(cerrors.FailedPrecondition, "%s (origin group %q is used by route %q)",
		lastOriginMsg, group.Name, route)
}

// ListOrigins returns the origins under (rg, profile, originGroup).
//
//nolint:dupl // parallel to ListRoutes over distinct grandchild types and stores.
func (m *Mock) ListOrigins(_ context.Context, rg, profile, originGroup string) ([]driver.AzureFrontDoorOrigin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.originGroups.Has(childKey(rg, profile, originGroup)) {
		return nil, cerrors.Newf(cerrors.NotFound, "front door origin group %q not found", originGroup)
	}

	all := m.origins.SortedValues()
	out := make([]driver.AzureFrontDoorOrigin, 0, len(all))

	for i := range all {
		if sameParent(all[i].ResourceGroup, all[i].Profile, all[i].OriginGroup, rg, profile, originGroup) {
			out = append(out, cloneOrigin(all[i]))
		}
	}

	return out, nil
}

// CreateOrUpdateRoute validates r, applies the schema defaults and stores it
// under (rg, profile, endpoint, name) as a full replace. The parent endpoint must
// exist, r.OriginGroup must name an origin group in the same profile, and the
// route must not claim a domain+protocol+path another route on the endpoint
// already serves.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) CreateOrUpdateRoute(
	_ context.Context, rg, profile, endpoint, name string, r driver.AzureFrontDoorRoute,
) (*driver.AzureFrontDoorRoute, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "front door route name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ep, ok := m.endpoints.Get(childKey(rg, profile, endpoint))
	if !ok {
		return nil, false, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", endpoint)
	}

	existing, existed := m.routes.Get(grandchildKey(rg, profile, endpoint, name))

	var prev *driver.AzureFrontDoorRoute
	if existed {
		prev = &existing
	}

	out, err := m.writeRouteLocked(&ep, prev, name, r.OriginGroup, cloneAnyMap(r.Properties))
	if err != nil {
		return nil, false, err
	}

	return out, !existed, nil
}

// UpdateRoute overlays patch.Properties on the stored route (ARM PATCH) and
// re-validates the merged result, all under one lock. A non-empty
// patch.OriginGroup repoints the route; an empty one keeps the stored group.
//
//nolint:gocritic // hugeParam: value carries maps copied defensively below.
func (m *Mock) UpdateRoute(
	_ context.Context, rg, profile, endpoint, name string, patch driver.AzureFrontDoorRoute,
) (*driver.AzureFrontDoorRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.routes.Get(grandchildKey(rg, profile, endpoint, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door route %q not found", name)
	}

	ep, ok := m.endpoints.Get(childKey(rg, profile, endpoint))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", endpoint)
	}

	originGroup := patch.OriginGroup
	if originGroup == "" {
		originGroup = existing.OriginGroup
	}

	return m.writeRouteLocked(&ep, &existing, name, originGroup, overlay(existing.Properties, patch.Properties))
}

// writeRouteLocked validates and defaults props, resolves the origin group,
// refuses a domain/protocol/path conflict and stores the route. prev is the
// stored route being replaced, or nil on create. Callers hold m.mu.
func (m *Mock) writeRouteLocked(
	ep *driver.AzureFrontDoorEndpoint, prev *driver.AzureFrontDoorRoute, name, originGroup string,
	props map[string]any,
) (*driver.AzureFrontDoorRoute, error) {
	if props == nil {
		props = map[string]any{}
	}

	if originGroup == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "front door route properties.originGroup is required")
	}

	if err := validateRoute(props); err != nil {
		return nil, err
	}

	group, ok := m.originGroups.Get(childKey(ep.ResourceGroup, ep.Profile, originGroup))
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"front door route references origin group %q, which does not exist in profile %q", originGroup, ep.Profile)
	}

	applyRouteDefaults(props)

	if err := m.checkRouteConflict(ep, name, props); err != nil {
		return nil, err
	}

	stored := driver.AzureFrontDoorRoute{
		Name:          name,
		ResourceGroup: ep.ResourceGroup,
		Profile:       ep.Profile,
		Endpoint:      ep.Name,
		OriginGroup:   group.Name,
		Properties:    props,
		ETag:          idgen.UUID(),
	}

	if prev != nil {
		stored.Name = prev.Name
	}

	m.routes.Set(grandchildKey(ep.ResourceGroup, ep.Profile, ep.Name, name), stored)

	out := cloneRoute(stored)

	return &out, nil
}

// checkRouteConflict returns FailedPrecondition when another route on ep already
// serves one of the (domain, protocol, path) combinations props claims: Azure
// requires that combination to be unique per route. The only domain cloudemu
// models is the endpoint's default domain (linkToDefaultDomain), since custom
// domain references are refused. Callers hold m.mu.
func (m *Mock) checkRouteConflict(ep *driver.AzureFrontDoorEndpoint, name string, props map[string]any) error {
	mine := routeClaims(props)
	if len(mine) == 0 {
		return nil
	}

	for _, other := range m.routes.SortedValues() {
		if !sameParent(other.ResourceGroup, other.Profile, other.Endpoint, ep.ResourceGroup, ep.Profile, ep.Name) ||
			strings.EqualFold(other.Name, name) {
			continue
		}

		for _, claim := range routeClaimList(other.Properties) {
			if _, clash := mine[claim]; clash {
				return cerrors.Newf(cerrors.FailedPrecondition, "%s Route %q already serves %s.",
					routeConflictMsg, other.Name, claim)
			}
		}
	}

	return nil
}

// routeClaims is the set form of routeClaimList.
func routeClaims(props map[string]any) map[string]struct{} {
	list := routeClaimList(props)
	out := make(map[string]struct{}, len(list))

	for _, c := range list {
		out[c] = struct{}{}
	}

	return out
}

// routeClaimList is the "domain protocol path" combinations a defaulted route
// serves, in a stable order. Paths compare case-insensitively.
func routeClaimList(props map[string]any) []string {
	if s, _ := props[linkToDefaultDomainKey].(string); !strings.EqualFold(s, stateEnabled) {
		return nil
	}

	protocols := stringList(props[supportedProtocolsKey])
	patterns := stringList(props[patternsToMatchKey])
	out := make([]string, 0, len(protocols)*len(patterns))

	for _, proto := range protocols {
		for _, path := range patterns {
			out = append(out, "default domain, "+strings.ToLower(proto)+", "+strings.ToLower(path))
		}
	}

	return out
}

// GetRoute returns the stored route.
func (m *Mock) GetRoute(_ context.Context, rg, profile, endpoint, name string) (*driver.AzureFrontDoorRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.routes.Get(grandchildKey(rg, profile, endpoint, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "front door route %q not found", name)
	}

	out := cloneRoute(r)

	return &out, nil
}

// DeleteRoute removes the stored route.
func (m *Mock) DeleteRoute(_ context.Context, rg, profile, endpoint, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.routes.Delete(grandchildKey(rg, profile, endpoint, name)) {
		return cerrors.Newf(cerrors.NotFound, "front door route %q not found", name)
	}

	return nil
}

// ListRoutes returns the routes under (rg, profile, endpoint).
//
//nolint:dupl // parallel to ListOrigins over distinct grandchild types and stores.
func (m *Mock) ListRoutes(_ context.Context, rg, profile, endpoint string) ([]driver.AzureFrontDoorRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.endpoints.Has(childKey(rg, profile, endpoint)) {
		return nil, cerrors.Newf(cerrors.NotFound, "front door endpoint %q not found", endpoint)
	}

	all := m.routes.SortedValues()
	out := make([]driver.AzureFrontDoorRoute, 0, len(all))

	for i := range all {
		if sameParent(all[i].ResourceGroup, all[i].Profile, all[i].Endpoint, rg, profile, endpoint) {
			out = append(out, cloneRoute(all[i]))
		}
	}

	return out, nil
}

// sameParent reports whether a grandchild's (rg, profile, parent) matches the
// wanted triple, case-insensitively (ARM names are case-insensitive).
func sameParent(rg, profile, parent, wantRG, wantProfile, wantParent string) bool {
	return strings.EqualFold(rg, wantRG) && strings.EqualFold(profile, wantProfile) &&
		strings.EqualFold(parent, wantParent)
}

// routeReferencing returns the name of the first route in (rg, profile) that
// forwards to originGroup, or "" when none does. Callers hold m.mu.
func (m *Mock) routeReferencing(rg, profile, originGroup string) string {
	for _, r := range m.routes.SortedValues() {
		if sameParent(r.ResourceGroup, r.Profile, r.OriginGroup, rg, profile, originGroup) {
			return r.Name
		}
	}

	return ""
}

// purgeOrigins deletes every origin under (rg, profile, originGroup). An empty
// originGroup matches every origin in the profile. Callers hold m.mu.
func (m *Mock) purgeOrigins(rg, profile, originGroup string) {
	for _, o := range m.origins.SortedValues() {
		if !strings.EqualFold(o.ResourceGroup, rg) || !strings.EqualFold(o.Profile, profile) {
			continue
		}

		if originGroup == "" || strings.EqualFold(o.OriginGroup, originGroup) {
			m.origins.Delete(grandchildKey(o.ResourceGroup, o.Profile, o.OriginGroup, o.Name))
		}
	}
}

// purgeRoutes deletes every route under (rg, profile, endpoint). An empty
// endpoint matches every route in the profile. Callers hold m.mu.
func (m *Mock) purgeRoutes(rg, profile, endpoint string) {
	for _, r := range m.routes.SortedValues() {
		if !strings.EqualFold(r.ResourceGroup, rg) || !strings.EqualFold(r.Profile, profile) {
			continue
		}

		if endpoint == "" || strings.EqualFold(r.Endpoint, endpoint) {
			m.routes.Delete(grandchildKey(r.ResourceGroup, r.Profile, r.Endpoint, r.Name))
		}
	}
}

// validateOrigin applies the schema's checks to an origin: hostName is required
// (AFDOriginProperties.required), the ports, priority and weight must be integers
// in their ranges, enabledState is an enum and enforceCertificateNameCheck a
// boolean. Each optional key is checked only when supplied.
func validateOrigin(props map[string]any) error {
	if s, _ := props[hostNameKey].(string); s == "" {
		return cerrors.New(cerrors.InvalidArgument, "front door origin properties.hostName is required")
	}

	ranges := []struct {
		key      string
		min, max int64
	}{
		{httpPortKey, minPort, maxPort},
		{httpsPortKey, minPort, maxPort},
		{priorityKey, minPriority, maxPriority},
		{weightKey, minWeight, maxWeight},
	}

	for _, r := range ranges {
		if err := checkRange(props, r.key, r.min, r.max); err != nil {
			return err
		}
	}

	if v, ok := props[enforceCertNameCheckKey]; ok && v != nil {
		if _, isBool := v.(bool); !isBool {
			return cerrors.New(cerrors.InvalidArgument,
				"front door origin properties.enforceCertificateNameCheck must be a boolean")
		}
	}

	return checkEnum(props, "origin", enabledStateKey, []string{stateEnabled, stateDisabled})
}

// applyOriginDefaults fills the values Azure reports for omitted origin fields:
// httpPort 80, httpsPort 443 and enforceCertificateNameCheck true are the
// schema defaults; enabledState is reported as Enabled.
func applyOriginDefaults(props map[string]any) {
	setDefault(props, httpPortKey, defaultHTTPPort)
	setDefault(props, httpsPortKey, defaultHTTPSPort)
	setDefault(props, enforceCertNameCheckKey, true)
	setDefault(props, enabledStateKey, stateEnabled)
}

// originEnabled reports whether an origin's enabledState is not Disabled.
func originEnabled(props map[string]any) bool {
	s, _ := props[enabledStateKey].(string)

	return !strings.EqualFold(s, stateDisabled)
}

// validateRoute applies the schema's checks to a route: the enums, the
// supportedProtocols list and the patternsToMatch prefix, each only when
// supplied. It also refuses customDomains and ruleSets references: cloudemu does
// not model either resource, so no such reference can resolve.
func validateRoute(props map[string]any) error {
	enums := []struct {
		key     string
		allowed []string
	}{
		{forwardingProtocolKey, []string{"HttpOnly", "HttpsOnly", forwardMatchRequest}},
		{httpsRedirectKey, []string{stateEnabled, stateDisabled}},
		{linkToDefaultDomainKey, []string{stateEnabled, stateDisabled}},
		{enabledStateKey, []string{stateEnabled, stateDisabled}},
	}

	for _, e := range enums {
		if err := checkEnum(props, "route", e.key, e.allowed); err != nil {
			return err
		}
	}

	allowed := []string{protocolHTTP, protocolHTTPS}

	if err := eachString(props, supportedProtocolsKey, func(s string) bool { return containsFold(allowed, s) },
		"front door route properties.supportedProtocols entries must be Http or Https"); err != nil {
		return err
	}

	if err := eachString(props, patternsToMatchKey, func(s string) bool { return strings.HasPrefix(s, "/") },
		"front door route properties.patternsToMatch entries must start with /"); err != nil {
		return err
	}

	for _, key := range []string{customDomainsKey, ruleSetsKey} {
		if err := refuseUnmodeledRefs(props, key); err != nil {
			return err
		}
	}

	return nil
}

// refuseUnmodeledRefs rejects a non-empty reference list under key.
func refuseUnmodeledRefs(props map[string]any, key string) error {
	v, ok := props[key]
	if !ok || v == nil {
		return nil
	}

	list, isList := v.([]any)
	if !isList {
		return cerrors.Newf(cerrors.InvalidArgument, "front door route properties.%s must be a list", key)
	}

	if len(list) == 0 {
		return nil
	}

	id := ""
	if ref, isMap := list[0].(map[string]any); isMap {
		id, _ = ref["id"].(string)
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"front door route properties.%s references %q, which does not exist (cloudemu does not model %s yet)",
		key, id, key)
}

// applyRouteDefaults fills the schema defaults for omitted route fields:
// forwardingProtocol MatchRequest, httpsRedirect and linkToDefaultDomain
// Disabled, supportedProtocols [Http, Https]; enabledState is reported as
// Enabled.
func applyRouteDefaults(props map[string]any) {
	setDefault(props, forwardingProtocolKey, forwardMatchRequest)
	setDefault(props, httpsRedirectKey, stateDisabled)
	setDefault(props, linkToDefaultDomainKey, stateDisabled)
	setDefault(props, supportedProtocolsKey, []any{protocolHTTP, protocolHTTPS})
	setDefault(props, enabledStateKey, stateEnabled)
}

// checkRange rejects a numeric property outside [lo, hi] or not an integer. An
// absent key passes.
func checkRange(props map[string]any, key string, lo, hi int64) error {
	v, ok := props[key]
	if !ok || v == nil {
		return nil
	}

	n, isNum := toFloat(v)
	if !isNum || n < float64(lo) || n > float64(hi) || n != float64(int64(n)) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"front door origin properties.%s must be an integer between %d and %d", key, lo, hi)
	}

	return nil
}

// toFloat reads a JSON-decoded (float64) or Go-typed integer value.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// checkEnum rejects a string property that is not one of allowed. An absent key
// passes.
func checkEnum(props map[string]any, kind, key string, allowed []string) error {
	v, ok := props[key]
	if !ok || v == nil {
		return nil
	}

	if s, isStr := v.(string); isStr && containsFold(allowed, s) {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"front door %s properties.%s must be one of %s", kind, key, strings.Join(allowed, ", "))
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

// stringList returns the string entries of a []any value.
func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))

	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}

	return false
}

// setDefault stores v under key when the key is absent or null.
func setDefault(props map[string]any, key string, v any) {
	if cur, ok := props[key]; !ok || cur == nil {
		props[key] = v
	}
}

// overlay deep-copies base and replaces each top-level key patch supplies (ARM
// PATCH merge semantics).
func overlay(base, patch map[string]any) map[string]any {
	out := cloneAnyMap(base)
	if out == nil {
		out = make(map[string]any, len(patch))
	}

	for k, v := range patch {
		out[k] = cloneAnyValue(v)
	}

	return out
}
