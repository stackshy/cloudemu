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
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const resourceGroupType = "Microsoft.Resources/resourceGroups"

// Path-segment counts for the routes this handler serves.
const (
	partsCollection = 3 // /subscriptions/{sub}/resourcegroups
	partsGroup      = 4 // /subscriptions/{sub}/resourcegroups/{name}
	partsExport     = 5 // /subscriptions/{sub}/resourcegroups/{name}/exportTemplate
	partsOperation  = 4 // /subscriptions/{sub}/operationresults/{id}
)

// Handler serves the resource-group collection and its members.
type Handler struct {
	mu sync.RWMutex
	// groups is keyed subscription -> lowercased-name -> group body. Names are
	// stored lowercased because ARM resolves a resource group case-insensitively
	// (create "myRG", get "MYRG"); the body keeps the original-cased name.
	groups map[string]map[string]map[string]any
}

// New returns a resource-group handler.
func New() *Handler {
	return &Handler{groups: map[string]map[string]map[string]any{}}
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

	// Real ARM deletes a resource group asynchronously: 202 with a Location the
	// SDK polls until it returns a terminal status. The delete already ran in
	// memory, so the poll endpoint reports success immediately.
	w.Header().Set("Location", absoluteURL(r, "/subscriptions/"+sub+"/operationresults/"+strings.ToLower(name)))
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
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

	// The emulator does not track per-group resource membership here, so the
	// exported template is a valid, empty ARM template skeleton.
	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"template": map[string]any{
			"$schema":        "https://schema.management.azure.com/schemas/2015-01-01/deploymentTemplate.json#",
			"contentVersion": "1.0.0.0",
			"parameters":     map[string]any{},
			"variables":      map[string]any{},
			"resources":      []any{},
			"outputs":        map[string]any{},
		},
	})
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
