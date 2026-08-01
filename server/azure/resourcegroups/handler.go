// Package resourcegroups implements the Azure Resource Manager resource-group
// API.
//
// Every Azure resource lives in a resource group, so a caller creates one
// before anything else and deletes it last. Without the API the first step of
// any provisioning run fails and nothing behind it is reachable.
package resourcegroups

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// Handler serves the resource-group collection and its members.
type Handler struct {
	mu     sync.RWMutex
	groups map[string]map[string]json.RawMessage // subscription -> name -> body
}

// New returns a resource-group handler.
func New() *Handler {
	return &Handler{groups: map[string]map[string]json.RawMessage{}}
}

// path splits /subscriptions/{sub}/resourcegroups[/{name}], case-insensitively
// on the collection segment because callers spell it both ways.
func path(urlPath string) (sub, name string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "subscriptions") {
		return "", "", false
	}

	if !strings.EqualFold(parts[2], "resourcegroups") {
		return "", "", false
	}

	// Anything deeper belongs to a resource handler, not this one.
	if len(parts) > 4 {
		return "", "", false
	}

	if len(parts) == 4 {
		return parts[1], parts[3], true
	}

	return parts[1], "", true
}

// Matches claims resource-group requests only. A path that continues into
// /providers/... is a resource inside the group and belongs elsewhere.
func (*Handler) Matches(r *http.Request) bool {
	_, _, ok := path(r.URL.Path)

	return ok
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sub, name, ok := path(r.URL.Path)
	if !ok {
		writeErr(w, http.StatusBadRequest, "InvalidPath", "malformed resource group path")
		return
	}

	if name == "" {
		if r.Method == http.MethodGet {
			h.list(w, sub)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.put(w, r, sub, name)
	case http.MethodGet, http.MethodHead:
		h.get(w, sub, name)
	case http.MethodDelete:
		h.remove(w, sub, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request, sub, name string) {
	// Capped like every sibling ARM handler; the decode stays tolerant
	// because a bare resource-group PUT carries no body.
	r.Body = http.MaxBytesReader(w, r.Body, azurearm.MaxBodyBytes)

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}

	body["id"] = "/subscriptions/" + sub + "/resourceGroups/" + name
	body["name"] = name
	body["type"] = "Microsoft.Resources/resourceGroups"

	// A group is usable the moment it is created; there is no provisioning to
	// wait on, and a caller that polls would never see it leave a pending state.
	body["properties"] = map[string]any{"provisioningState": "Succeeded"}

	raw, err := json.Marshal(body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	h.mu.Lock()
	if h.groups[sub] == nil {
		h.groups[sub] = map[string]json.RawMessage{}
	}

	h.groups[sub][name] = raw
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, raw)
}

func (h *Handler) get(w http.ResponseWriter, sub, name string) {
	h.mu.RLock()
	raw, ok := h.groups[sub][name]
	h.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, "ResourceGroupNotFound",
			"Resource group '"+name+"' could not be found.")

		return
	}

	writeJSON(w, http.StatusOK, raw)
}

func (h *Handler) list(w http.ResponseWriter, sub string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	value := make([]json.RawMessage, 0, len(h.groups[sub]))
	for _, raw := range h.groups[sub] {
		value = append(value, raw)
	}

	writeJSON(w, http.StatusOK, mustMarshal(map[string]any{"value": value}))
}

func (h *Handler) remove(w http.ResponseWriter, sub, name string) {
	h.mu.Lock()
	_, existed := h.groups[sub][name]
	delete(h.groups[sub], name)
	h.mu.Unlock()

	// Deleting a group that is already gone is the caller's desired end state,
	// and a teardown retry must not fail on its second pass.
	if !existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return raw
}

func writeJSON(w http.ResponseWriter, status int, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, mustMarshal(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	}))
}
