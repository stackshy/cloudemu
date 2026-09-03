package loadbalancer

// External L7 HTTP(S) load-balancer front-end chain:
//
//	forwardingRule → targetHttp(s)Proxy → urlMap → backendService → instanceGroup
//
// The three front-end resource types (targetHttpProxies, targetHttpsProxies,
// sslCertificates) and the instance-group backends (instanceGroups,
// regionInstanceGroups) have no cross-provider driver model, so they reuse the
// same opaque GCPComputeResourceStore path as healthChecks/urlMaps/targetPools:
// the decoded insert body round-trips verbatim, with server identity layered on
// read. On top of plain CRUD they add the L7 action verbs a real user calls to
// wire the chain — setUrlMap / setSslCertificates on a proxy, and
// addInstances / removeInstances / listInstances on an instance group.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// L7 front-end and instance-group collections served through the opaque store.
const (
	resourceTargetHTTPProxies    = "targetHttpProxies"
	resourceTargetHTTPSProxies   = "targetHttpsProxies"
	resourceSslCertificates      = "sslCertificates"
	resourceInstanceGroups       = "instanceGroups"
	resourceRegionInstanceGroups = "regionInstanceGroups"
)

// POST action verbs on a named L7 resource.
const (
	actionSetURLMap          = "setUrlMap"
	actionSetSslCertificates = "setSslCertificates"
	actionAddInstances       = "addInstances"
	actionRemoveInstances    = "removeInstances"
	actionListInstances      = "listInstances"
)

// Reserved body members and the field keys the opaque L7 resources use.
const (
	// reservedBodyPrefix marks stored body members that must never be emitted on
	// the wire (they are not real GCP fields). gcpResourceJSON strips them.
	reservedBodyPrefix = "cloudemu:"
	// igMembersKey holds an instance group's membership (a list of instance URLs)
	// inside the opaque body. Stripped from every response by gcpResourceJSON.
	igMembersKey = "cloudemu:igMembers"
	// fieldURLMap / fieldSslCertificates are the proxy body members the setUrlMap
	// and setSslCertificates actions mutate.
	fieldURLMap          = "urlMap"
	fieldSslCertificates = "sslCertificates"
)

// routeL7Resource dispatches the L7 front-end resources: the POST action verbs
// (setUrlMap, setSslCertificates, addInstances, removeInstances, listInstances)
// on a named resource, and otherwise the shared opaque-resource CRUD.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeL7Resource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName != "" && rp.Action != "" && r.Method == http.MethodPost {
		switch rp.Action {
		case actionSetURLMap:
			h.setProxyField(w, r, rp, fieldURLMap, false)
		case actionSetSslCertificates:
			h.setProxyField(w, r, rp, fieldSslCertificates, true)
		case actionAddInstances:
			h.mutateInstanceGroupMembers(w, r, rp, true)
		case actionRemoveInstances:
			h.mutateInstanceGroupMembers(w, r, rp, false)
		case actionListInstances:
			h.listInstanceGroupInstances(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	h.routeGCPResource(w, r, rp)
}

// setProxyRequest is the union of the two proxy set-* action bodies: a single
// urlMap reference (setUrlMap) or a list of certificate URLs (setSslCertificates).
type setProxyRequest struct {
	URLMap          string   `json:"urlMap,omitempty"`
	SslCertificates []string `json:"sslCertificates,omitempty"`
}

// setProxyField applies compute.targetHttp(s)Proxies.setUrlMap /
// setSslCertificates: it stores the reference onto the proxy's body so a
// subsequent Get reflects it and the chain resolves. list selects between the
// scalar urlMap and the sslCertificates list.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setProxyField(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, field string, list bool) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	var req setProxyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.validateProxySetRequest(r.Context(), rp, &req, list); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	err := store.UpdateGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName,
		func(res *lbdriver.GCPResource) {
			if res.Body == nil {
				res.Body = map[string]any{}
			}

			if list {
				res.Body[field] = toAnySlice(req.SslCertificates)
			} else {
				res.Body[field] = req.URLMap
			}
		})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName, rp.Action)
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// instanceRef is one entry of an addInstances/removeInstances body's instances[].
type instanceRef struct {
	Instance string `json:"instance,omitempty"`
}

// instancesRequest is the addInstances/removeInstances request body.
type instancesRequest struct {
	Instances []instanceRef `json:"instances,omitempty"`
}

// mutateInstanceGroupMembers applies compute.instanceGroups.addInstances /
// removeInstances by editing the instance group's membership stored in its
// opaque body. Membership drives backendServices.getHealth and the group's
// reported size.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) mutateInstanceGroupMembers(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, add bool) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	var req instancesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	changed := make([]string, 0, len(req.Instances))

	for i := range req.Instances {
		if req.Instances[i].Instance != "" {
			changed = append(changed, req.Instances[i].Instance)
		}
	}

	err := store.UpdateGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName,
		func(res *lbdriver.GCPResource) {
			if res.Body == nil {
				res.Body = map[string]any{}
			}

			res.Body[igMembersKey] = toAnySlice(applyMembership(membersOf(res.Body), changed, add))
		})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	verb := actionAddInstances
	if !add {
		verb = actionRemoveInstances
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName, verb)
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// listInstanceGroupInstances serves compute.instanceGroups.listInstances,
// returning the group's members as a compute#instanceGroupsListInstances
// envelope (each member's instance URL), the shape the SDK iterator reads.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listInstanceGroupInstances(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	res, err := store.GetGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	members := membersOf(res.Body)

	items := make([]map[string]any, 0, len(members))
	for _, m := range members {
		items = append(items, map[string]any{"instance": m, "status": "RUNNING"})
	}

	envelope := map[string]any{
		"kind":     "compute#instanceGroupsListInstances",
		"id":       "projects/" + rp.Project + "/" + listScopeSegment(rp) + "/instanceGroups/" + rp.ResourceName,
		"items":    items,
		"selfLink": gcprest.SelfLink(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName),
	}

	gcprest.WriteJSON(w, http.StatusOK, envelope)
}

// applyMembership adds or removes changed from current, preserving order and
// deduplicating on add.
func applyMembership(current, changed []string, add bool) []string {
	if add {
		return dedupeAppend(current, changed)
	}

	drop := make(map[string]struct{}, len(changed))
	for _, c := range changed {
		drop[c] = struct{}{}
	}

	out := make([]string, 0, len(current))

	for _, c := range current {
		if _, ok := drop[c]; !ok {
			out = append(out, c)
		}
	}

	return out
}

// dedupeAppend appends each of add to base, skipping values already present.
func dedupeAppend(base, add []string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, b := range base {
		seen[b] = struct{}{}
	}

	out := base

	for _, a := range add {
		if _, ok := seen[a]; ok {
			continue
		}

		seen[a] = struct{}{}

		out = append(out, a)
	}

	return out
}

// membersOf reads an instance group's stored membership, tolerating both the
// []string set in-process and the []any produced by a JSON round-trip (persist).
func membersOf(body map[string]any) []string {
	raw, ok := body[igMembersKey]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))

		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}

		return out
	default:
		return nil
	}
}

// toAnySlice widens a []string to []any so it survives snapshot JSON encoding
// the same way a client-supplied list would.
func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}

	return out
}

// singularOf renders the GCP error-message resource noun for a collection (e.g.
// "targetHttpProxies" → "target_http_proxy"), matching real GCP's
// resourceInUseByAnotherResource phrasing.
func singularOf(collection string) string {
	switch collection {
	case resourceHealthChecks:
		return "health_check"
	case resourceURLMaps:
		return "url_map"
	case resourceSslCertificates:
		return "ssl_certificate"
	case resourceTargetHTTPProxies:
		return "target_http_proxy"
	case resourceTargetHTTPSProxies:
		return "target_https_proxy"
	case resourceInstanceGroups, resourceRegionInstanceGroups:
		return "instance_group"
	default:
		return strings.TrimSuffix(collection, "s")
	}
}

// gcpResourceInUse returns the name of a resource still referencing the opaque
// resource being deleted, or "" when the delete is safe. It centralizes the
// per-collection reference-integrity guards so no delete orphans a dependent.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) gcpResourceInUse(ctx context.Context, rp gcprest.ResourcePath) string {
	switch rp.ResourceType {
	case resourceHealthChecks:
		return h.healthCheckInUse(ctx, rp)
	case resourceURLMaps:
		return h.opaqueRefsField(ctx, rp, []string{resourceTargetHTTPProxies, resourceTargetHTTPSProxies}, fieldURLMap)
	case resourceSslCertificates:
		return h.opaqueRefsField(ctx, rp, []string{resourceTargetHTTPSProxies}, fieldSslCertificates)
	case resourceTargetHTTPProxies, resourceTargetHTTPSProxies:
		return h.forwardingRuleRefTarget(ctx, rp.ResourceName)
	case resourceInstanceGroups, resourceRegionInstanceGroups:
		return h.backendServiceRefGroup(ctx, rp)
	default:
		return ""
	}
}

// opaqueRefsField returns the name of a same-scope opaque resource (in any of
// collections) whose body member field references rp.ResourceName by trailing
// name, or "" when none does. field may be a scalar string or a list of
// references.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) opaqueRefsField(ctx context.Context, rp gcprest.ResourcePath, collections []string, field string) string {
	store, ok := h.gcpStore()
	if !ok {
		return ""
	}

	for _, coll := range collections {
		items, err := store.ListGCPResources(ctx, coll, scopeKeyOf(rp))
		if err != nil {
			continue
		}

		for i := range items {
			if bodyFieldRefs(items[i].Body[field], rp.ResourceName) {
				return items[i].Name
			}
		}
	}

	return ""
}

// bodyFieldRefs reports whether a body member (a scalar reference or a list of
// them) resolves to name by trailing path segment.
func bodyFieldRefs(v any, name string) bool {
	switch t := v.(type) {
	case string:
		return lastPathSegment(t) == name
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && lastPathSegment(s) == name {
				return true
			}
		}
	case []string:
		for _, s := range t {
			if lastPathSegment(s) == name {
				return true
			}
		}
	}

	return false
}

// forwardingRuleRefTarget returns the name of a forwarding rule whose target
// resolves to proxyName, or "" when none does.
func (h *Handler) forwardingRuleRefTarget(ctx context.Context, proxyName string) string {
	lbs, err := h.lb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return ""
	}

	for i := range lbs {
		if lastPathSegment(lbs[i].Tags[frTargetTag]) == proxyName {
			return displayName(lbs[i].Tags, frNameTag, lbs[i].Name)
		}
	}

	return ""
}

// backendServiceRefGroup returns the name of a backend service whose backends[]
// reference the instance group identified by rp (scope + name), or "" when none does.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) backendServiceRefGroup(ctx context.Context, rp gcprest.ResourcePath) string {
	tgs, err := h.lb.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return ""
	}

	for i := range tgs {
		var backends []backend

		decodeJSONTag(tgs[i].Tags, bsBackendsTag, &backends)

		for j := range backends {
			if backends[j].Group == "" {
				continue
			}

			// Match the group's scope AND name, not just the trailing name: a
			// same-named instance group in a different zone/region must not be
			// falsely reported as in use.
			_, scope, name := parseGroupRef(backends[j].Group)
			if name == rp.ResourceName && scope == rp.ScopeName {
				return displayName(tgs[i].Tags, bsNameTag, tgs[i].Name)
			}
		}
	}

	return ""
}

// sortedMembers returns a stable-ordered copy of an instance group's members
// for deterministic health reporting.
func sortedMembers(members []string) []string {
	out := append([]string(nil), members...)
	sort.Strings(out)

	return out
}
