package monitor

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const diagSuffix = "/providers/microsoft.insights/diagnosticsettings"

// diagType is the ARM type returned for a diagnostic setting.
const diagType = "Microsoft.Insights/diagnosticSettings"

// DiagnosticSettingsHandler serves microsoft.insights/diagnosticSettings, an
// extension resource that hangs off any resource URI (a VM, a workspace, a
// storage account). It has no portable equivalent, so it is stored in the
// handler's own map keyed by the target resource URI and setting name.
type DiagnosticSettingsHandler struct {
	mu sync.RWMutex
	m  map[string]map[string]map[string]any // resourceUri -> name -> properties
}

// NewDiagnosticSettingsHandler returns an empty diagnostic-settings handler.
func NewDiagnosticSettingsHandler() *DiagnosticSettingsHandler {
	return &DiagnosticSettingsHandler{m: make(map[string]map[string]map[string]any)}
}

// Matches claims any path whose (lowercased) form carries the
// /providers/microsoft.insights/diagnosticSettings segment.
func (*DiagnosticSettingsHandler) Matches(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.URL.Path), diagSuffix)
}

// split returns the target resource URI and the (possibly empty) setting name.
func splitDiagPath(path string) (uri, name string) {
	i := strings.Index(strings.ToLower(path), diagSuffix)
	if i < 0 {
		return strings.Trim(path, "/"), ""
	}

	uri = strings.Trim(path[:i], "/")
	name = strings.Trim(path[i+len(diagSuffix):], "/")

	return uri, name
}

func (h *DiagnosticSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uri, name := splitDiagPath(r.URL.Path)

	if name == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.list(w, uri)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.put(w, r, uri, name)
	case http.MethodGet:
		h.get(w, uri, name)
	case http.MethodDelete:
		h.delete(w, uri, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// diagRequest is the inbound body; only properties are meaningful.
type diagRequest struct {
	Properties map[string]any `json:"properties"`
}

func (h *DiagnosticSettingsHandler) put(w http.ResponseWriter, r *http.Request, uri, name string) {
	var req diagRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	props := req.Properties
	if props == nil {
		props = map[string]any{}
	}

	h.mu.Lock()

	byName := h.m[uri]
	if byName == nil {
		byName = make(map[string]map[string]any)
		h.m[uri] = byName
	}

	byName[name] = props

	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, diagJSON(uri, name, props))
}

func (h *DiagnosticSettingsHandler) get(w http.ResponseWriter, uri, name string) {
	h.mu.RLock()
	props, ok := h.m[uri][name]
	h.mu.RUnlock()

	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "diagnosticSetting "+name+" not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, diagJSON(uri, name, props))
}

func (h *DiagnosticSettingsHandler) delete(w http.ResponseWriter, uri, name string) {
	h.mu.Lock()

	byName := h.m[uri]

	_, ok := byName[name]
	if ok {
		delete(byName, name)
	}

	h.mu.Unlock()

	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "diagnosticSetting "+name+" not found")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *DiagnosticSettingsHandler) list(w http.ResponseWriter, uri string) {
	h.mu.RLock()
	byName := h.m[uri]

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}

	sort.Strings(names)

	value := make([]map[string]any, 0, len(byName))
	for _, name := range names {
		value = append(value, diagJSON(uri, name, byName[name]))
	}
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

func diagJSON(uri, name string, props map[string]any) map[string]any {
	return map[string]any{
		"id":         uri + "/providers/microsoft.insights/diagnosticSettings/" + name,
		"name":       name,
		"type":       diagType,
		"properties": props,
	}
}
