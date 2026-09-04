package loadbalancer

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// GCP Compute Load Balancing resources that the portable LoadBalancer model
// can't express — healthChecks and urlMaps (global) and targetPools (regional).
// They are stored verbatim through the GCPComputeResourceStore optional
// capability and re-emitted with server-injected identity so every field the
// client sent round-trips (create → read/list → delete), which is all Terraform
// and the compute SDK need to provision an L4/L7 load balancer.
const (
	resourceHealthChecks = "healthChecks"
	resourceTargetPools  = "targetPools"
	resourceURLMaps      = "urlMaps"
)

// resourceKind maps a collection to its compute#… item kind.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var resourceKind = map[string]string{
	resourceHealthChecks:         "compute#healthCheck",
	resourceTargetPools:          "compute#targetPool",
	resourceURLMaps:              "compute#urlMap",
	resourceTargetHTTPProxies:    "compute#targetHttpProxy",
	resourceTargetHTTPSProxies:   "compute#targetHttpsProxy",
	resourceSslCertificates:      "compute#sslCertificate",
	resourceInstanceGroups:       "compute#instanceGroup",
	resourceRegionInstanceGroups: "compute#instanceGroup",
}

// gcpStore returns the store capability, or false when the backing driver does
// not implement it (non-GCP driver wired in by mistake).
func (h *Handler) gcpStore() (lbdriver.GCPComputeResourceStore, bool) {
	s, ok := h.lb.(lbdriver.GCPComputeResourceStore)

	return s, ok
}

// scopeKeyOf collapses a parsed path's scope to the store scope key: "global"
// for global resources, the region name for regional ones.
//
//nolint:gocritic // rp is a request-scoped value
func scopeKeyOf(rp gcprest.ResourcePath) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return gcprest.ScopeGlobal
	}

	return rp.ScopeName
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertGCPResource(w, r, rp)
		case http.MethodGet:
			h.listGCPResource(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getGCPResource(w, r, rp)
	case http.MethodPatch:
		h.mutateGCPResource(w, r, rp, true)
	case http.MethodPut:
		h.mutateGCPResource(w, r, rp, false)
	case http.MethodDelete:
		h.deleteGCPResource(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// mutateGCPResource serves compute *.patch (merge=true) and *.update
// (merge=false) for the opaque resources. Patch merges the request body's
// members onto the stored body; update replaces the body wholesale. Both return
// a DONE Operation, matching real GCP, so Terraform apply-on-change succeeds
// instead of hitting a 405.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) mutateGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, merge bool) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	var body map[string]any
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if err := h.validateGCPResourceRefs(r.Context(), rp, body); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	err := store.UpdateGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName,
		func(res *lbdriver.GCPResource) { applyBody(res, body, merge) })
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	verb := "update"
	if merge {
		verb = "patch"
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName, verb)
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// applyBody merges (patch) or replaces (update) a stored resource's body with
// the request body, always preserving the resource name so a body that omits or
// changes it can't orphan the record from its store key.
func applyBody(res *lbdriver.GCPResource, body map[string]any, merge bool) {
	if !merge || res.Body == nil {
		res.Body = map[string]any{}
	}

	for k, v := range body {
		res.Body[k] = v
	}

	res.Body["name"] = res.Name
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	var body map[string]any
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	name, _ := body["name"].(string)
	if name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}

	if err := h.validateGCPResourceRefs(r.Context(), rp, body); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	res := lbdriver.GCPResource{
		Collection:        rp.ResourceType,
		Scope:             scopeKeyOf(rp),
		Name:              name,
		ID:                numericID(rp.ResourceType + "/" + name),
		CreationTimestamp: time.Now().UTC().Format(time.RFC3339),
		Body:              body,
	}

	if err := store.PutGCPResource(r.Context(), res); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, name, "insert")
	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
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

	gcprest.WriteJSON(w, http.StatusOK, gcpResourceJSON(res, rp, hostOf(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	items, err := store.ListGCPResources(r.Context(), rp.ResourceType, scopeKeyOf(rp))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	filter := r.URL.Query().Get("filter")

	matched := make([]lbdriver.GCPResource, 0, len(items))

	for i := range items {
		if gcprest.NameMatches(filter, items[i].Name) {
			matched = append(matched, items[i])
		}
	}

	sort.SliceStable(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	page, err := pagination.Paginate(matched, r.URL.Query().Get("pageToken"),
		gcprest.MaxResults(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	host := hostOf(r)
	out := make([]map[string]any, 0, len(page.Items))

	for i := range page.Items {
		scope := rp
		scope.ResourceName = page.Items[i].Name
		out = append(out, gcpResourceJSON(&page.Items[i], scope, host))
	}

	envelope := map[string]any{
		"kind":     resourceKind[rp.ResourceType] + "List",
		"id":       "projects/" + rp.Project + "/" + listScopeSegment(rp) + "/" + rp.ResourceType,
		"items":    out,
		"selfLink": gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, ""),
	}
	if page.NextPageToken != "" {
		envelope["nextPageToken"] = page.NextPageToken
	}

	gcprest.WriteJSON(w, http.StatusOK, envelope)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	// Real GCP refuses to delete a resource still referenced by another (400
	// resourceInUseByAnotherResource); deleting it here would orphan the
	// dependent — a backend service pointing at a missing health check, a target
	// proxy at a missing url-map, an https proxy at a missing certificate, a
	// backend service at a missing instance group, or a forwarding rule at a
	// missing proxy.
	if ref := h.gcpResourceInUse(r.Context(), rp); ref != "" {
		gcprest.WriteError(w, http.StatusBadRequest, reasonResourceInUse,
			"The "+singularOf(rp.ResourceType)+" resource '"+rp.ResourceName+"' is already being used by '"+ref+"'")

		return
	}

	if err := store.DeleteGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName, "delete")
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// gcpResourceJSON re-emits a stored resource: the verbatim body with server
// identity (kind/id/selfLink/creationTimestamp) layered on top.
//
//nolint:gocritic // rp is a request-scoped value
func gcpResourceJSON(res *lbdriver.GCPResource, rp gcprest.ResourcePath, host string) map[string]any {
	out := make(map[string]any, len(res.Body)+internalFieldCount)

	for k, v := range res.Body {
		// Reserved internal members (e.g. instance-group membership) are stored in
		// the body but must never leak onto the wire — they are not real GCP fields.
		if strings.HasPrefix(k, reservedBodyPrefix) {
			continue
		}

		out[k] = v
	}

	out["kind"] = resourceKind[res.Collection]
	out["id"] = res.ID
	out["name"] = res.Name
	out["creationTimestamp"] = res.CreationTimestamp
	out["selfLink"] = gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, res.Collection, res.Name)

	switch rp.Scope {
	case gcprest.ScopeRegions:
		out["region"] = host + "/compute/v1/projects/" + rp.Project + "/regions/" + rp.ScopeName
	case gcprest.ScopeZones:
		out["zone"] = host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName
	}

	// An instance group reports its member count as size, mirroring real GCP so a
	// client can see membership changes without listing instances.
	if res.Collection == resourceInstanceGroups || res.Collection == resourceRegionInstanceGroups {
		out["size"] = len(membersOf(res.Body))
	}

	return out
}

// internalFieldCount is the number of server-injected members gcpResourceJSON
// adds on top of the stored body (kind, id, name, creationTimestamp, selfLink,
// region/zone, size).
const internalFieldCount = 7

// healthCheckInUse returns the name of a same-scope backend service whose
// healthChecks[] references the health check being deleted, or "" when none
// does.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) healthCheckInUse(ctx context.Context, rp gcprest.ResourcePath) string {
	tgs, err := h.lb.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return ""
	}

	scope := scopeKeyOf(rp)

	for i := range tgs {
		if tgs[i].Tags[bsScopeTag] != scope {
			continue
		}

		refs := tgs[i].Tags[bsHealthChecksTag]
		if refs == "" {
			continue
		}

		for _, ref := range strings.Split(refs, ",") {
			if lastPathSegment(ref) == rp.ResourceName {
				return displayName(tgs[i].Tags, bsNameTag, tgs[i].Name)
			}
		}
	}

	return ""
}

// lastPathSegment returns the trailing name of a GCP self-link or relative
// reference (e.g. ".../global/healthChecks/hc1" → "hc1"), or the input when it
// has no separator.
func lastPathSegment(ref string) string {
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		return ref[idx+1:]
	}

	return ref
}

// listScopeSegment renders the scope path segment used in a list envelope's id.
//
//nolint:gocritic // rp is a request-scoped value
func listScopeSegment(rp gcprest.ResourcePath) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return gcprest.ScopeGlobal
	}

	// rp.Scope is already the collection segment ("zones" or "regions"); use it
	// verbatim so a zonal list envelope's id isn't mislabeled as "regions/...".
	return rp.Scope + "/" + rp.ScopeName
}
