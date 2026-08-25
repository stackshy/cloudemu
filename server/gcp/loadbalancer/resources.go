package loadbalancer

import (
	"net/http"
	"time"

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
	resourceHealthChecks: "compute#healthCheck",
	resourceTargetPools:  "compute#targetPool",
	resourceURLMaps:      "compute#urlMap",
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

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
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
	case http.MethodDelete:
		h.deleteGCPResource(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
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

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, name, "insert")
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

	host := hostOf(r)
	out := make([]map[string]any, 0, len(items))

	for i := range items {
		scope := rp
		scope.ResourceName = items[i].Name
		out = append(out, gcpResourceJSON(&items[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":     resourceKind[rp.ResourceType] + "List",
		"id":       "projects/" + rp.Project + "/" + listScopeSegment(rp) + "/" + rp.ResourceType,
		"items":    out,
		"selfLink": gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteGCPResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	if err := store.DeleteGCPResource(r.Context(), rp.ResourceType, scopeKeyOf(rp), rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, rp.ResourceType, rp.ResourceName, "delete")
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// gcpResourceJSON re-emits a stored resource: the verbatim body with server
// identity (kind/id/selfLink/creationTimestamp) layered on top.
//
//nolint:gocritic // rp is a request-scoped value
func gcpResourceJSON(res *lbdriver.GCPResource, rp gcprest.ResourcePath, host string) map[string]any {
	out := make(map[string]any, len(res.Body)+internalFieldCount)
	for k, v := range res.Body {
		out[k] = v
	}

	out["kind"] = resourceKind[res.Collection]
	out["id"] = res.ID
	out["name"] = res.Name
	out["creationTimestamp"] = res.CreationTimestamp
	out["selfLink"] = gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, res.Collection, res.Name)

	if rp.Scope == gcprest.ScopeRegions {
		out["region"] = host + "/compute/v1/projects/" + rp.Project + "/regions/" + rp.ScopeName
	}

	return out
}

// internalFieldCount is the number of server-injected members gcpResourceJSON
// adds on top of the stored body (kind, id, name, creationTimestamp, selfLink,
// region).
const internalFieldCount = 6

// listScopeSegment renders the scope path segment used in a list envelope's id.
//
//nolint:gocritic // rp is a request-scoped value
func listScopeSegment(rp gcprest.ResourcePath) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return gcprest.ScopeGlobal
	}

	return gcprest.ScopeRegions + "/" + rp.ScopeName
}
