// Package tags serves the Azure Tags resource-provider API
// (Microsoft.Resources/tags/default): the scope-level tag set an armresources
// TagsClient manages through CreateOrUpdateAtScope / GetAtScope / UpdateAtScope
// / DeleteAtScope.
//
// The tag set is addressed by an opaque {scope} prefix: a subscription
// (subscriptions/{sub}), a resource group or any resource id, followed by the
// fixed suffix /providers/Microsoft.Resources/tags/default. As in real ARM, the
// tags at a resource-group or resource scope are that resource's own tags: the
// handler reads them with a GET of the resource and writes them with a tags
// PATCH, through the ARM router (SetRouter), so the resource's GET and the Tags
// API always agree and locks apply. Subscription tag sets have no resource
// behind them in the emulator and live in the persisted
// providers/azure/tagsatscope store.
//
// Every operation is synchronous and answers HTTP 200, matching the armresources
// TagsClient, which treats any non-200 as an error (DeleteAtScope included).
package tags

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/providers/azure/tagsatscope"
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

// Handler serves Microsoft.Resources/tags/default requests. Resource-group and
// resource scopes go to the resource itself through router; subscription scopes
// use store. mu keeps a read-modify-write atomic.
type Handler struct {
	mu     sync.Mutex
	store  *tagsatscope.Mock
	router http.Handler
}

// New returns a tags-at-scope handler over store. A nil store gives the handler
// a private one.
func New(store *tagsatscope.Mock) *Handler {
	if store == nil {
		store = tagsatscope.New()
	}

	return &Handler{store: store}
}

// SetRouter installs the ARM router the handler reads and writes resource and
// resource-group tags through. Without one, every scope uses the store.
func (h *Handler) SetRouter(router http.Handler) {
	h.router = router
}

// PurgeResourceGroup drops the tag sets at and under subscription/
// resourceGroup, backing the resource-group cascade delete.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
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

	if h.router != nil && isResourceScope(scope) {
		h.serveResource(w, r, scope)
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
	h.store.Set(scope, stored)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, response(scope, stored))
}

// get returns the current tag set at scope (GetAtScope). An unknown scope has an
// empty set, matching real ARM (there is no "not found" for a scope's tags).
func (h *Handler) get(w http.ResponseWriter, scope string) {
	stored := h.store.Get(scope)

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
	result := applyPatch(h.store.Get(scope), op, body.Properties.Tags)
	h.store.Set(scope, result)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, response(scope, cloneTags(result)))
}

// delete clears the tag set at scope (DeleteAtScope). Idempotent: an unknown
// scope still answers 200.
func (h *Handler) delete(w http.ResponseWriter, scope string) {
	h.mu.Lock()
	h.store.Delete(scope)
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
		// as ARM does: the value in the request body is ignored.
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

// isResourceScope reports whether scope is a resource group or a resource in
// one (subscriptions/{sub}/resourceGroups/{rg}[/...]).
func isResourceScope(scope string) bool {
	parts := strings.Split(scope, "/")

	return len(parts) >= 4 && strings.EqualFold(parts[0], "subscriptions") && strings.EqualFold(parts[2], "resourcegroups")
}

// serveResource applies a tags request to the resource at scope: GET reads its
// tags, PUT/PATCH/DELETE compute the new set and write it with a tags PATCH on
// the resource. A failure on the resource (404 for a missing one, 409 for a
// locked one) is returned as the resource answered it.
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, scope string) {
	var update func(current map[string]string) map[string]string

	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body tagsBody
		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		update = func(map[string]string) map[string]string { return cloneTags(body.Properties.Tags) }
	case http.MethodPatch:
		var body tagsPatchBody
		if !azurearm.DecodeJSON(w, r, &body) {
			return
		}

		op := body.Operation
		if op == "" {
			op = opMerge
		}

		if !strings.EqualFold(op, opMerge) && !strings.EqualFold(op, opReplace) && !strings.EqualFold(op, opDelete) {
			azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "unsupported tags patch operation: "+op)
			return
		}

		update = func(cur map[string]string) map[string]string { return applyPatch(cur, op, body.Properties.Tags) }
	case http.MethodDelete:
		update = func(map[string]string) map[string]string { return map[string]string{} }
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rec := h.call(r, http.MethodGet, scope, nil)
	if rec.Code != http.StatusOK {
		passThrough(w, rec)
		return
	}

	tags := tagsIn(rec.Body.Bytes())

	if update != nil {
		tags = update(tags)

		body, _ := json.Marshal(map[string]any{"tags": tags})

		if rec = h.call(r, http.MethodPatch, scope, body); rec.Code >= http.StatusMultipleChoices {
			passThrough(w, rec)
			return
		}
	}

	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusOK)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, response(scope, tags))
}

// call sends method to the resource at scope through the ARM router, keeping
// the caller's query (api-version), host and credentials.
func (h *Handler) call(r *http.Request, method, scope string, body []byte) *httptest.ResponseRecorder {
	req := r.Clone(r.Context())
	req.Method = method
	req.URL.Path = "/" + scope
	req.URL.RawPath = ""
	req.RequestURI = ""
	req.Body = http.NoBody
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")

	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	return rec
}

// tagsIn reads the top-level tags of an ARM resource body.
func tagsIn(body []byte) map[string]string {
	var res struct {
		Tags map[string]string `json:"tags"`
	}

	_ = json.Unmarshal(body, &res)

	return cloneTags(res.Tags)
}

func passThrough(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}

	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
