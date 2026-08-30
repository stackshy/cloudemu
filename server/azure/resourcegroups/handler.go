// Package resourcegroups implements the Azure Resource Manager resource-group
// API.
//
// Every Azure resource lives in a resource group, so a caller creates one
// before anything else and deletes it last. Without the API the first step of
// any provisioning run fails and nothing behind it is reachable.
//
// The handler models the real lifecycle a client depends on: a create returns
// 201 (an update 200) and requires a location; lookups and updates are case-
// insensitive on the group name; PATCH merges tags; delete is an async
// long-running operation (202 + Location) the SDK polls; and exportTemplate
// returns an ARM template skeleton.
package resourcegroups

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/azure/resourcegraph"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// templateAPIVersion is the apiVersion stamped onto every resource entry in an
// exported template. Real ARM resolves this per resource type from the
// provider's registered API versions; the emulator has no such registry, so —
// mirroring the same fixed-apiVersion approach the VM capture template already
// uses (server/azure/virtualmachines/instances.go's captureResult) — every
// entry gets one reasonable, recent Resource Manager API version rather than a
// second per-type table to maintain.
const templateAPIVersion = "2021-04-01"

const resourceGroupType = "Microsoft.Resources/resourceGroups"

// Path-segment counts for the routes this handler serves.
const (
	partsCollection = 3 // /subscriptions/{sub}/resourcegroups
	partsGroup      = 4 // /subscriptions/{sub}/resourcegroups/{name}
	partsExport     = 5 // /subscriptions/{sub}/resourcegroups/{name}/exportTemplate
	partsOperation  = 4 // /subscriptions/{sub}/operationresults/{id}
)

// ResourceGroupPurger deletes every resource one service handler owns in a given
// resource group. It backs the resource-group cascade delete: an RG is a pure
// container, so deleting it must delete the resources created under it rather
// than leaving them as globally addressable orphans. Each per-service ARM
// handler that stores its resources resource-group-scoped (compute, networking,
// load balancing, storage today) implements it; a handler that does not is
// simply not passed to New, so its resource type is not cascaded (a documented,
// extensible gap).
type ResourceGroupPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

// Handler serves the resource-group collection and its members.
type Handler struct {
	mu sync.RWMutex
	// groups is keyed subscription -> lowercased-name -> group body. Names are
	// stored lowercased because ARM resolves a resource group case-insensitively
	// (create "myRG", get "MYRG"); the body keeps the original-cased name.
	groups map[string]map[string]map[string]any
	// engine backs exportTemplate: the emulator tracks group membership by the
	// resource group segment already embedded in each resource's own id, so
	// enumerating "what's in this group" means walking the same cross-service
	// inventory Resource Graph and the generic resources listing already read
	// from. Nil disables exportTemplate's resource enumeration (it still
	// returns a valid, empty template) — callers that don't wire discovery.
	engine *resourcediscovery.Engine
	// purgers cascade a resource-group delete into the resources it contains.
	purgers []ResourceGroupPurger
}

// New returns a resource-group handler. engine is the cross-service inventory
// engine exportTemplate enumerates a group's resources from; nil is accepted
// (exportTemplate then reports an empty resources[]). purgers cascade a group
// delete into the resources created under it; pass the per-service handlers
// that own resource-group-scoped resources.
func New(engine *resourcediscovery.Engine, purgers ...ResourceGroupPurger) *Handler {
	return &Handler{groups: map[string]map[string]map[string]any{}, engine: engine, purgers: purgers}
}

// routeKind classifies a resource-group request path.
type routeKind int

const (
	kindNone       routeKind = iota
	kindCollection           // /subscriptions/{sub}/resourcegroups
	kindGroup                // /subscriptions/{sub}/resourcegroups/{name}
	kindExport               // /subscriptions/{sub}/resourcegroups/{name}/exportTemplate
	kindOperation            // /subscriptions/{sub}/operationresults/{id}
)

// parse classifies urlPath, case-insensitively on the collection segment
// because callers spell "resourceGroups" and "resourcegroups" both ways.
func parse(urlPath string) (kind routeKind, sub, name string) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < partsCollection || !strings.EqualFold(parts[0], "subscriptions") {
		return kindNone, "", ""
	}

	sub = parts[1]

	// Async operation-result poll for a resource-group delete.
	if len(parts) == partsOperation && strings.EqualFold(parts[2], "operationresults") {
		return kindOperation, sub, parts[3]
	}

	if !strings.EqualFold(parts[2], "resourcegroups") {
		return kindNone, "", ""
	}

	switch len(parts) {
	case partsCollection:
		return kindCollection, sub, ""
	case partsGroup:
		return kindGroup, sub, parts[3]
	case partsExport:
		if strings.EqualFold(parts[4], "exportTemplate") {
			return kindExport, sub, parts[3]
		}
	}

	return kindNone, "", ""
}

// Matches claims resource-group requests and the delete operation-result poll.
// A path that continues into /providers/... is a resource inside the group (and
// a .../resources path is the generic resources list) — both belong elsewhere.
func (*Handler) Matches(r *http.Request) bool {
	kind, _, _ := parse(r.URL.Path)

	return kind != kindNone
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	kind, sub, name := parse(r.URL.Path)

	switch kind {
	case kindCollection:
		h.serveCollection(w, r, sub)
	case kindGroup:
		h.serveGroup(w, r, sub, name)
	case kindExport:
		h.serveExport(w, r, sub, name)
	case kindOperation:
		// The delete already ran synchronously; the poll is always terminal.
		w.WriteHeader(http.StatusOK)
	case kindNone:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown resource-group path")
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, sub string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.list(w, sub)
}

func (h *Handler) serveGroup(w http.ResponseWriter, r *http.Request, sub, name string) {
	switch r.Method {
	case http.MethodPut:
		h.put(w, r, sub, name)
	case http.MethodPatch:
		h.patch(w, r, sub, name)
	case http.MethodGet, http.MethodHead:
		h.get(w, sub, name)
	case http.MethodDelete:
		h.remove(w, r, sub, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request, sub, name string) {
	var body map[string]any
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	// A resource group requires a location that cannot be changed later; real
	// ARM rejects a create/update with no location.
	loc, _ := body["location"].(string)
	if strings.TrimSpace(loc) == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "LocationRequired",
			"The location property is required for this definition.")

		return
	}

	group := map[string]any{
		"id":         azureGroupID(sub, name),
		"name":       name,
		"type":       resourceGroupType,
		"location":   loc,
		"properties": map[string]any{"provisioningState": "Succeeded"},
	}

	if tags, ok := body["tags"]; ok {
		group["tags"] = tags
	}

	if mb, ok := body["managedBy"].(string); ok && mb != "" {
		group["managedBy"] = mb
	}

	status := http.StatusCreated
	if h.store(sub, name, group) {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, group)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request, sub, name string) {
	var body map[string]any
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	group, ok := h.groups[sub][strings.ToLower(name)]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceGroupNotFound",
			"Resource group '"+name+"' could not be found.")

		return
	}

	// PATCH replaces the tags and managedBy fields when supplied and leaves the
	// rest (location, provisioningState) untouched.
	if tags, present := body["tags"]; present {
		group["tags"] = tags
	}

	if mb, present := body["managedBy"].(string); present {
		group["managedBy"] = mb
	}

	azurearm.WriteJSON(w, http.StatusOK, group)
}

func (h *Handler) get(w http.ResponseWriter, sub, name string) {
	h.mu.RLock()
	group, ok := h.groups[sub][strings.ToLower(name)]
	h.mu.RUnlock()

	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceGroupNotFound",
			"Resource group '"+name+"' could not be found.")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, group)
}

func (h *Handler) list(w http.ResponseWriter, sub string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	value := make([]map[string]any, 0, len(h.groups[sub]))
	for _, group := range h.groups[sub] {
		value = append(value, group)
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request, sub, name string) {
	h.mu.Lock()
	_, existed := h.groups[sub][strings.ToLower(name)]
	delete(h.groups[sub], strings.ToLower(name))
	h.mu.Unlock()

	// Deleting a group that is already gone is the caller's desired end state,
	// and a teardown retry must not fail on its second pass. There is nothing to
	// poll, so answer synchronously.
	if !existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Cascade: a resource group is a container, so deleting it deletes every
	// resource created under it. Each purger tears down its own service's
	// resources in this group; a purge failure is best-effort (the group is
	// already gone and the ARM delete contract is a fire-and-forget 202), so it
	// does not fail the response.
	h.cascade(r.Context(), sub, name)

	// Real ARM deletes a resource group asynchronously: 202 with a Location the
	// SDK polls until it returns a terminal status. The delete already ran in
	// memory, so the poll endpoint reports success immediately.
	w.Header().Set("Location", absoluteURL(r, "/subscriptions/"+sub+"/operationresults/"+strings.ToLower(name)))
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// cascade fans a resource-group delete out to every registered purger so the
// resources created under the group are torn down with it. Best-effort: a
// purger error is swallowed (there is no channel to report it on an async
// fire-and-forget delete, and a partial teardown must not block the rest).
func (h *Handler) cascade(ctx context.Context, sub, name string) {
	for _, p := range h.purgers {
		_ = p.PurgeResourceGroup(ctx, sub, name)
	}
}

// exportTemplateRequest is the exportTemplate POST body: see
// https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/export-template
// ("ExportTemplateRequest"). Resources is an allowlist of resource IDs to
// export, or a single "*" entry for the whole group; Options is a CSV of
// parameterization flags this emulator does not act on (the export already
// bakes in literal names rather than parameterizing them).
type exportTemplateRequest struct {
	Resources []string `json:"resources"`
	Options   string   `json:"options"`
}

func (h *Handler) serveExport(w http.ResponseWriter, r *http.Request, sub, name string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "POST required")
		return
	}

	h.mu.RLock()
	_, ok := h.groups[sub][strings.ToLower(name)]
	h.mu.RUnlock()

	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceGroupNotFound",
			"Resource group '"+name+"' could not be found.")

		return
	}

	req := decodeExportRequest(r)

	resources, err := h.exportResources(r.Context(), name, req.Resources)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"template": map[string]any{
			"$schema":        "https://schema.management.azure.com/schemas/2015-01-01/deploymentTemplate.json#",
			"contentVersion": "1.0.0.0",
			"parameters":     map[string]any{},
			"variables":      map[string]any{},
			"resources":      resources,
			"outputs":        map[string]any{},
		},
	})
}

// decodeExportRequest best-effort decodes the exportTemplate POST body. Both
// fields are optional per the ARM contract (a bare POST with no body is a
// valid "export everything" request), so a missing or malformed body is
// treated as no filter rather than failing the request.
func decodeExportRequest(r *http.Request) exportTemplateRequest {
	var req exportTemplateRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	return req
}

// exportResources enumerates the resources belonging to group, rendered as
// ARM template resource entries. The emulator has no dedicated group-
// membership store: group ownership is read off the same resource-group
// segment already embedded in every resource's id, via the cross-service
// discovery engine that Resource Graph and the generic resources listing
// (GET .../resourceGroups/{rg}/resources) already read from — so exportTemplate
// stays consistent with what those two report the group contains. A nil
// engine (discovery not wired) yields an empty slice, matching the previous
// always-empty behavior for callers who don't opt in to discovery.
func (h *Handler) exportResources(ctx context.Context, group string, filter []string) ([]map[string]any, error) {
	out := []map[string]any{}

	if h.engine == nil {
		return out, nil
	}

	all, err := h.engine.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	byID := !wantsAllResources(filter)

	for i := range all {
		res := &all[i]
		if !strings.EqualFold(resourcegraph.ResourceGroupOf(res.ARN), group) {
			continue
		}

		if byID && !containsFold(filter, res.ARN) {
			continue
		}

		out = append(out, exportResourceEntry(res))
	}

	return out, nil
}

// wantsAllResources reports whether an exportTemplate request's resources
// filter selects the whole group: no filter was supplied, or it names the "*"
// wildcard the ARM contract documents for "export everything".
func wantsAllResources(resources []string) bool {
	if len(resources) == 0 {
		return true
	}

	for _, id := range resources {
		if id == "*" {
			return true
		}
	}

	return false
}

// containsFold reports whether want is present in list, case-insensitively —
// ARM resource IDs are case-insensitive.
func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}

	return false
}

// exportResourceEntry renders one discovered resource as an ARM template
// resource entry: type + apiVersion + name + location + properties, the
// fields every real exportTemplate response carries (id/subscriptionId are
// not part of a template resource — a template is meant to be redeployable
// under any subscription/resource group). properties defaults to an empty
// object rather than being omitted, matching the shape of a real response.
func exportResourceEntry(r *resourcediscovery.Resource) map[string]any {
	entry := map[string]any{
		"type":       resourcegraph.AzureType(r.Service, r.Type),
		"apiVersion": templateAPIVersion,
		"name":       r.ID,
		"properties": map[string]any{},
	}

	if r.Region != "" {
		entry["location"] = r.Region
	}

	if len(r.Properties) > 0 {
		entry["properties"] = r.Properties
	}

	// Drop the internal "cloudemu:" ARM-bookkeeping tags so the exported template
	// carries only real user tags, matching what Resource Graph renders.
	if tags := resourcegraph.StripInternalTags(r.Tags); len(tags) > 0 {
		entry["tags"] = tags
	}

	return entry
}

// store writes the group under a case-insensitive key and reports whether a
// group of that name already existed (an update rather than a create).
func (h *Handler) store(sub, name string, group map[string]any) (existed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.groups[sub] == nil {
		h.groups[sub] = map[string]map[string]any{}
	}

	_, existed = h.groups[sub][strings.ToLower(name)]
	h.groups[sub][strings.ToLower(name)] = group

	return existed
}

func azureGroupID(sub, name string) string {
	return "/subscriptions/" + sub + "/resourceGroups/" + name
}

// absoluteURL builds an absolute URL for path from the incoming request, so a
// Location header the SDK poller follows resolves correctly.
func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host + path
}
