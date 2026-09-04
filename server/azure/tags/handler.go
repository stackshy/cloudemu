// Package tags serves the Azure Tags resource-provider API
// (Microsoft.Resources/tags/default): the scope-level tag set an armresources
// TagsClient manages through CreateOrUpdateAtScope / GetAtScope / UpdateAtScope
// / DeleteAtScope.
//
// The tag set is addressed by an opaque {scope} prefix — a subscription
// (subscriptions/{sub}) or any resource id — followed by the fixed suffix
// /providers/Microsoft.Resources/tags/default. The handler owns its own
// in-memory store keyed by that scope; there is no driver, because tags-at-scope
// is a universal ARM overlay rather than a per-service resource.
//
// Every operation is synchronous and answers HTTP 200, matching the armresources
// TagsClient, which treats any non-200 as an error (DeleteAtScope included).
package tags

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	// pathSuffix is the fixed tail every tags-at-scope URL carries. The scope is
	// whatever precedes it.
	pathSuffix = "/providers/Microsoft.Resources/tags/default"
	// resourceName and resourceType populate the ARM response envelope.
	resourceName = "default"
	resourceType = "Microsoft.Resources/tags"

	opMerge   = "Merge"
	opReplace = "Replace"
	opDelete  = "Delete"
)

// Handler serves Microsoft.Resources/tags/default requests. It is
// self-contained: the tag sets live in its own store, one per scope.
type Handler struct {
	mu      sync.RWMutex
	byScope map[string]map[string]string
}

// New returns a tags-at-scope handler with an empty store.
func New() *Handler {
	return &Handler{byScope: make(map[string]map[string]string)}
}

// Matches reports whether r targets a tags-at-scope URL. The suffix is matched
// case-insensitively (SDK URL templates and hand-written tooling differ in
// casing) and is disjoint from every other Azure handler, so registration order
// is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := scopeOf(r.URL.Path)

	return ok
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "not a tags-at-scope path")
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.put(w, r, scope)
	case http.MethodGet:
		h.get(w, scope)
	case http.MethodPatch:
		h.patch(w, r, scope)
	case http.MethodDelete:
		h.delete(w, scope)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// put replaces the entire tag set at scope (CreateOrUpdateAtScope).
func (h *Handler) put(w http.ResponseWriter, r *http.Request, scope string) {
	var body tagsBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored := cloneTags(body.Properties.Tags)

	h.mu.Lock()
	h.byScope[scope] = stored
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, response(scope, stored))
}

// get returns the current tag set at scope (GetAtScope). An unknown scope has an
// empty set, matching real ARM (there is no "not found" for a scope's tags).
func (h *Handler) get(w http.ResponseWriter, scope string) {
	h.mu.RLock()
	stored := cloneTags(h.byScope[scope])
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, response(scope, stored))
}

// patch applies Merge / Replace / Delete against the current set (UpdateAtScope).
func (h *Handler) patch(w http.ResponseWriter, r *http.Request, scope string) {
	var body tagsPatchBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	op := body.Operation
	if op == "" {
		op = opMerge
	}

	if !strings.EqualFold(op, opMerge) && !strings.EqualFold(op, opReplace) && !strings.EqualFold(op, opDelete) {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "unsupported tags patch operation: "+body.Operation)
		return
	}

	h.mu.Lock()
	result := applyPatch(h.byScope[scope], op, body.Properties.Tags)
	h.byScope[scope] = result
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, response(scope, cloneTags(result)))
}

// delete clears the tag set at scope (DeleteAtScope). Idempotent: an unknown
// scope still answers 200.
func (h *Handler) delete(w http.ResponseWriter, scope string) {
	h.mu.Lock()
	delete(h.byScope, scope)
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// applyPatch computes the new tag set for op against current. current may be nil.
func applyPatch(current map[string]string, op string, in map[string]string) map[string]string {
	switch {
	case strings.EqualFold(op, opReplace):
		return cloneTags(in)
	case strings.EqualFold(op, opDelete):
		out := cloneTags(current)
		// Delete removes each named tag regardless of the supplied value, exactly
		// as ARM does — the value in the request body is ignored.
		for k := range in {
			delete(out, k)
		}

		return out
	default: // Merge: add new keys, overwrite existing ones, keep the rest.
		out := cloneTags(current)
		for k, v := range in {
			out[k] = v
		}

		return out
	}
}

// response builds the ARM tags-at-scope envelope for scope with the given tags.
func response(scope string, tags map[string]string) tagsBody {
	if tags == nil {
		tags = map[string]string{}
	}

	return tagsBody{
		ID:         "/" + scope + pathSuffix,
		Name:       resourceName,
		Type:       resourceType,
		Properties: tagsProps{Tags: tags},
	}
}

// cloneTags returns an independent copy so stored sets never alias request or
// response maps. A nil input yields an empty (non-nil) map.
func cloneTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// scopeOf extracts the {scope} prefix from a tags-at-scope path, or ok=false
// when urlPath is not a tags-at-scope URL. The scope is normalized (no leading
// or trailing slash) so the same scope keys identically regardless of how the
// caller formatted it.
func scopeOf(urlPath string) (scope string, ok bool) {
	trimmed := strings.TrimRight(urlPath, "/")

	idx := lastIndexFold(trimmed, pathSuffix)
	if idx < 0 || idx+len(pathSuffix) != len(trimmed) {
		return "", false
	}

	scope = strings.Trim(trimmed[:idx], "/")
	if scope == "" {
		return "", false
	}

	return scope, true
}

// lastIndexFold is strings.LastIndex with case-insensitive matching of substr.
func lastIndexFold(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if strings.EqualFold(s[i:i+len(substr)], substr) {
			return i
		}
	}

	return -1
}
