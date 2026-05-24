package cloudasset

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/resourcediscovery"
)

// Path prefix shared by every Cloud Asset operation.
const apiPrefix = "/v1/"

// Custom-method names that may appear after a colon in the URL — e.g.,
// /v1/projects/X:searchAllResources.
const (
	methodSearchAllResources    = "searchAllResources"
	methodSearchAllIamPolicies  = "searchAllIamPolicies"
	methodExportAssets          = "exportAssets"
	methodBatchGetAssetsHistory = "batchGetAssetsHistory"
)

// Body size cap for any JSON request — matches the firestore handler.
const maxBodyBytes = 5 << 20

// Custom-method dispatch table. Immutable; declared as a package-level
// var because Go does not allow const maps.
//
//nolint:gochecknoglobals // immutable lookup set.
var customMethods = map[string]struct{}{
	methodSearchAllResources:    {},
	methodSearchAllIamPolicies:  {},
	methodExportAssets:          {},
	methodBatchGetAssetsHistory: {},
}

// Sentinel feed error so handler tests can match exactly without %w wrap.
var errFeedNotFound = errors.New("feed not found")

// Handler serves Cloud Asset Inventory v1 REST requests.
type Handler struct {
	engine    *resourcediscovery.Engine
	projectID string

	mu         sync.RWMutex
	feeds      map[string]*feed          // keyed by full feed name (projects/X/feeds/Y)
	operations map[string]map[string]any // keyed by op name; caches completed export results
}

type feed struct {
	Name        string            `json:"name"`
	AssetNames  []string          `json:"assetNames,omitempty"`
	AssetTypes  []string          `json:"assetTypes,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	FeedOutput  map[string]any    `json:"feedOutputConfig,omitempty"`
	Condition   map[string]any    `json:"condition,omitempty"`
}

// New returns a Cloud Asset handler backed by engine. projectID is used
// for feed-name validation and asset scoping; if empty, the engine's own
// AccountID (which holds the GCP project ID for GCP engines) is used so
// callers don't have to repeat it.
func New(engine *resourcediscovery.Engine, projectID string) *Handler {
	if projectID == "" && engine != nil {
		projectID = engine.AccountID()
	}

	return &Handler{
		engine:     engine,
		projectID:  projectID,
		feeds:      make(map[string]*feed),
		operations: make(map[string]map[string]any),
	}
}

// operationNamePrefix tags operation IDs we own so the Operations.Get
// path matcher (very broadly /v1/.../operations/...) only claims ours and
// not the GCE/compute operations served by other handlers.
const operationNamePrefix = "cloudemu-export-"

// Matches accepts paths that are unambiguously Cloud Asset. We match
// narrowly to avoid shadowing other GCP REST handlers (firestore claims
// /v1/projects/ broadly), so registration order is forgiving.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, apiPrefix) {
		return false
	}

	// Custom methods (colon syntax): /v1/{scope}:method.
	if i := strings.LastIndexByte(p, ':'); i > len(apiPrefix) {
		if _, ok := customMethods[p[i+1:]]; ok {
			return true
		}
	}

	// /v1/{parent}/operations/cloudemu-export-... — claim only operation
	// names this package creates; other handlers (compute, networks)
	// serve their own /operations/ paths.
	if strings.Contains(p, "/operations/"+operationNamePrefix) {
		return true
	}

	// /v1/{parent}/assets or /v1/{parent}/feeds[/{id}]
	return pathHasSegment(p, "assets") || pathHasSegment(p, "feeds")
}

func pathHasSegment(p, seg string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == seg {
			return true
		}
	}

	return false
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	if i := strings.LastIndexByte(p, ':'); i > len(apiPrefix) {
		method := p[i+1:]
		scope := stripAPIPrefix(p[:i])
		h.serveCustomMethod(w, r, scope, method)

		return
	}

	if endsWith, parent := segmentSuffix(p, "/assets"); endsWith {
		h.listAssets(w, r, stripAPIPrefix(parent))
		return
	}

	if endsWith, parent := segmentSuffix(p, "/feeds"); endsWith {
		h.serveFeedsCollection(w, r, stripAPIPrefix(parent))
		return
	}

	// /v1/{parent}/operations/cloudemu-export-... — the result of an
	// earlier exportAssets call. Cached in h.operations.
	if before, id, ok := segmentBeforeLast(p, "/operations/"); ok && strings.HasPrefix(id, operationNamePrefix) {
		h.getOperation(w, r, stripAPIPrefix(before)+"/operations/"+id)
		return
	}

	// /v1/{parent}/feeds/{id} — derive the canonical feed name without /v1/.
	if before, id, ok := segmentBeforeLast(p, "/feeds/"); ok {
		if strings.ContainsRune(id, '/') {
			writeError(w, http.StatusNotFound, "NOT_FOUND",
				"feed id cannot contain '/': "+id)

			return
		}

		h.serveFeedItem(w, r, stripAPIPrefix(before)+"/feeds/"+id)

		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown Cloud Asset path: "+p)
}

// stripAPIPrefix removes the leading "/v1/" so derived names match the
// canonical GCP shape (projects/X/feeds/Y, not /v1/projects/X/feeds/Y).
func stripAPIPrefix(p string) string {
	return strings.TrimPrefix(p, apiPrefix)
}

// expectedMethod returns the HTTP method the real Cloud Asset API uses for
// each custom method. Search and history are GETs; export is a POST
// long-running operation.
func expectedMethod(method string) string {
	if method == methodExportAssets {
		return http.MethodPost
	}

	return http.MethodGet
}

func (h *Handler) serveCustomMethod(w http.ResponseWriter, r *http.Request, scope, method string) {
	if want := expectedMethod(method); r.Method != want {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			want+" required for "+method)

		return
	}

	switch method {
	case methodSearchAllResources:
		h.searchAllResources(w, r, scope)
	case methodSearchAllIamPolicies:
		h.searchAllIamPolicies(w, r, scope)
	case methodExportAssets:
		h.exportAssets(w, r, scope)
	case methodBatchGetAssetsHistory:
		h.batchGetAssetsHistory(w, r, scope)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+method)
	}
}

// ----- searchAllResources -----

func (h *Handler) searchAllResources(w http.ResponseWriter, r *http.Request, _ string) {
	// Real Cloud Asset takes filter as a query param OR body field. Support
	// both; body wins if both supplied.
	body := readJSONBody(r)

	filter := r.URL.Query().Get("query")
	if v, ok := body["query"].(string); ok && v != "" {
		filter = v
	}

	pageSize := intParam(r, body, "pageSize")

	parsed := parseFilter(filter)
	if parsed.ForceEmpty {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}

	results, err := h.engine.List(r.Context(), parsed.Query)
	if err != nil {
		writeCErr(w, err)
		return
	}

	if pageSize > 0 && pageSize < len(results) {
		results = results[:pageSize]
	}

	out := make([]map[string]any, 0, len(results))
	for i := range results {
		out = append(out, resourceToSearchResult(&results[i], h.projectID))
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// ----- searchAllIamPolicies -----

func (*Handler) searchAllIamPolicies(w http.ResponseWriter, _ *http.Request, _ string) {
	// IAM policy walking is out of scope for Phase 4 — the engine doesn't
	// expose iam driver state. Return empty so callers can probe the API
	// without erroring out.
	writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
}

// ----- exportAssets -----

func (h *Handler) exportAssets(w http.ResponseWriter, r *http.Request, scope string) {
	body := readJSONBody(r)

	parsed := parseFilter(strOrEmpty(body["filter"]))

	var results []resourcediscovery.Resource

	if !parsed.ForceEmpty {
		var err error

		results, err = h.engine.List(r.Context(), parsed.Query)
		if err != nil {
			writeCErr(w, err)
			return
		}
	}

	op := h.buildAndCacheExportOperation(scope, results)
	writeJSON(w, http.StatusOK, op)
}

// buildAndCacheExportOperation creates the LRO response and caches it under
// its name so a subsequent Operations.Get can find it. The operation is
// scope-prefixed (projects/X/operations/cloudemu-export-...) so the path
// matcher in ServeHTTP routes the poll back to this handler.
func (h *Handler) buildAndCacheExportOperation(scope string, results []resourcediscovery.Resource) map[string]any {
	if scope == "" {
		scope = "projects/" + h.projectID
	}

	name := scope + "/operations/" + operationNamePrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	op := completedExportOperation(name, results)

	h.mu.Lock()
	h.operations[name] = op
	h.mu.Unlock()

	return op
}

// completedExportOperation builds the synchronous LRO response shape.
// Real Cloud Asset is async — it returns an operation name that the
// caller polls via Operations.Get. The mock returns done=true with the
// asset list inline so most callers (which call .Do() and check Done)
// work without polling, AND caches the response under name so callers
// that DO poll via Operations.Get find a matching entry.
func completedExportOperation(name string, results []resourcediscovery.Resource) map[string]any {
	assets := make([]map[string]any, 0, len(results))
	for i := range results {
		assets = append(assets, resourceToAsset(&results[i]))
	}

	return map[string]any{
		"name": name,
		"done": true,
		"metadata": map[string]any{
			"@type": "type.googleapis.com/google.cloud.asset.v1.ExportAssetsRequest",
		},
		"response": map[string]any{
			"@type":           "type.googleapis.com/google.cloud.asset.v1.ExportAssetsResponse",
			"readTime":        time.Now().UTC().Format(time.RFC3339Nano),
			"assets":          assets,
			"outputUriPrefix": "cloudemu://in-memory",
		},
	}
}

// getOperation serves Operations.Get for export operations cached by an
// earlier exportAssets call. Method is implicitly GET; we don't enforce
// because the dispatch already routed here for any verb on this path.
func (h *Handler) getOperation(w http.ResponseWriter, _ *http.Request, name string) {
	h.mu.RLock()
	op, ok := h.operations[name]
	h.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "operation not found: "+name)
		return
	}

	writeJSON(w, http.StatusOK, op)
}

// ----- batchGetAssetsHistory -----

func (h *Handler) batchGetAssetsHistory(w http.ResponseWriter, r *http.Request, _ string) {
	body := readJSONBody(r)

	parsed := parseFilter(strOrEmpty(body["filter"]))
	if parsed.ForceEmpty {
		writeJSON(w, http.StatusOK, map[string]any{"assets": []any{}})
		return
	}

	results, err := h.engine.List(r.Context(), parsed.Query)
	if err != nil {
		writeCErr(w, err)
		return
	}

	// Real API returns TemporalAsset entries with window timestamps; the
	// mock has no time-travel, so each asset gets a single window starting
	// at the read time.
	now := time.Now().UTC().Format(time.RFC3339Nano)

	out := make([]map[string]any, 0, len(results))
	for i := range results {
		out = append(out, map[string]any{
			"window": map[string]any{"startTime": now, "endTime": now},
			"asset":  resourceToAsset(&results[i]),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// ----- assets.list -----
//
// Real assets.list does not take a free-form filter expression. The SDK
// surfaces it as repeated assetTypes={...} query params plus pageSize.
// We honor assetTypes (any-of match) and ignore contentType / readTime
// since the mock has no time-travel or per-content-type projections.

func (h *Handler) listAssets(w http.ResponseWriter, r *http.Request, _ string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required for assets.list")
		return
	}

	assetTypes := r.URL.Query()["assetTypes"]
	pageSize := intParam(r, nil, "pageSize")

	allResults, err := h.collectAssetsForTypes(r.Context(), assetTypes)
	if err != nil {
		writeCErr(w, err)
		return
	}

	if pageSize > 0 && pageSize < len(allResults) {
		allResults = allResults[:pageSize]
	}

	out := make([]map[string]any, 0, len(allResults))
	for i := range allResults {
		out = append(out, resourceToAsset(&allResults[i]))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"assets":   out,
		"readTime": nowRFC(),
	})
}

// collectAssetsForTypes runs one engine query per assetType filter and
// merges the results. If no assetTypes are supplied, returns the full
// inventory in a single query.
func (h *Handler) collectAssetsForTypes(ctx context.Context, assetTypes []string) ([]resourcediscovery.Resource, error) {
	if len(assetTypes) == 0 {
		return h.engine.ListAll(ctx)
	}

	var all []resourcediscovery.Resource

	for _, at := range assetTypes {
		svc, typ := mapGCPAssetType(at)
		if svc == "" {
			continue
		}

		batch, err := h.engine.List(ctx, resourcediscovery.Query{
			Services: []string{svc},
			Type:     typ,
		})
		if err != nil {
			return nil, err
		}

		all = append(all, batch...)
	}

	return all, nil
}

// ----- Feeds collection (POST create, GET list) -----

func (h *Handler) serveFeedsCollection(w http.ResponseWriter, r *http.Request, parent string) {
	switch r.Method {
	case http.MethodPost:
		h.createFeed(w, r, parent)
	case http.MethodGet:
		h.listFeeds(w, r, parent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			"only POST and GET are supported on the feeds collection")
	}
}

func (h *Handler) createFeed(w http.ResponseWriter, r *http.Request, parent string) {
	body := readJSONBody(r)

	feedID := strOrEmpty(body["feedId"])
	if feedID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "feedId is required")
		return
	}

	feedBody, _ := body["feed"].(map[string]any)
	name := parent + "/feeds/" + feedID

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.feeds[name]; exists {
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "feed already exists: "+name)
		return
	}

	f := feedFromBody(feedBody)
	f.Name = name
	h.feeds[name] = f

	writeJSON(w, http.StatusOK, feedToWire(f))
}

func (h *Handler) listFeeds(w http.ResponseWriter, _ *http.Request, parent string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	prefix := parent + "/feeds/"
	out := make([]map[string]any, 0)

	for name, f := range h.feeds {
		if strings.HasPrefix(name, prefix) {
			out = append(out, feedToWire(f))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"feeds": out})
}

// ----- Feed item (GET, PATCH, DELETE) -----

func (h *Handler) serveFeedItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getFeed(w, name)
	case http.MethodPatch:
		h.patchFeed(w, r, name)
	case http.MethodDelete:
		h.deleteFeed(w, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
			"only GET, PATCH, DELETE are supported on a feed item")
	}
}

func (h *Handler) getFeed(w http.ResponseWriter, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	f, ok := h.feeds[name]
	if !ok {
		writeNotFound(w, name)
		return
	}

	writeJSON(w, http.StatusOK, feedToWire(f))
}

func (h *Handler) patchFeed(w http.ResponseWriter, r *http.Request, name string) {
	body := readJSONBody(r)

	// The PATCH body is an UpdateFeedRequest: { "feed": {...}, "updateMask": ... }.
	// Unwrap the inner feed before merging so partial updates land on the
	// right fields.
	feedBody, _ := body["feed"].(map[string]any)

	h.mu.Lock()
	defer h.mu.Unlock()

	f, ok := h.feeds[name]
	if !ok {
		writeNotFound(w, name)
		return
	}

	mergeFeed(f, feedBody)
	writeJSON(w, http.StatusOK, feedToWire(f))
}

func (h *Handler) deleteFeed(w http.ResponseWriter, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.feeds[name]; !ok {
		writeNotFound(w, name)
		return
	}

	delete(h.feeds, name)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ----- helpers -----

func writeNotFound(w http.ResponseWriter, name string) {
	writeError(w, http.StatusNotFound, "NOT_FOUND", errFeedNotFound.Error()+": "+name)
}

// segmentSuffix reports whether p ends with the given /segment.
// Returns ok and the path before the suffix.
func segmentSuffix(p, suffix string) (ok bool, before string) {
	if !strings.HasSuffix(p, suffix) {
		return false, ""
	}

	return true, strings.TrimSuffix(p, suffix)
}

// segmentBeforeLast splits p around the LAST occurrence of marker.
// Returns the part before, the part after, and ok=true if found.
func segmentBeforeLast(p, marker string) (before, after string, ok bool) {
	i := strings.LastIndex(p, marker)
	if i < 0 {
		return "", "", false
	}

	return p[:i], p[i+len(marker):], true
}

func readJSONBody(r *http.Request) map[string]any {
	out := map[string]any{}
	if r.Body == nil {
		return out
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil || len(raw) == 0 {
		return out
	}

	_ = json.Unmarshal(raw, &out)

	return out
}

func strOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

func intParam(r *http.Request, body map[string]any, key string) int {
	if v, ok := body[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}

	if s := r.URL.Query().Get(key); s != "" {
		n, err := strconv.Atoi(s)
		if err == nil {
			return n
		}
	}

	return 0
}

func nowRFC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// resourceToSearchResult formats one Resource for the searchAllResources
// response. GCP uses a "name" of //service/path.
func resourceToSearchResult(r *resourcediscovery.Resource, project string) map[string]any {
	return map[string]any{
		"name":        gcpResourceName(r),
		"assetType":   portableToGCPAssetType(r.Service, r.Type),
		"project":     "projects/" + project,
		"location":    r.Region,
		"displayName": r.ID,
		"labels":      labelsOrEmpty(r.Tags),
	}
}

// resourceToAsset formats one Resource for the exportAssets / batchGet
// response shape: { name, asset_type, resource: {...} }.
func resourceToAsset(r *resourcediscovery.Resource) map[string]any {
	return map[string]any{
		"name":      gcpResourceName(r),
		"assetType": portableToGCPAssetType(r.Service, r.Type),
		"resource": map[string]any{
			"version":              "v1",
			"discoveryDocumentUri": "",
			"discoveryName":        r.Type,
			"location":             r.Region,
			"data":                 map[string]any{"id": r.ID, "labels": labelsOrEmpty(r.Tags)},
		},
	}
}

// gcpResourceName builds the //service/path identifier GCP uses for assets.
// For most resources the engine's ARN is already a GCP self-link
// (projects/.../...), so prefix with the service host.
func gcpResourceName(r *resourcediscovery.Resource) string {
	host := portableServiceHost(r.Service)
	if !strings.HasPrefix(r.ARN, "//") {
		return "//" + host + "/" + r.ARN
	}

	return r.ARN
}

func portableServiceHost(service string) string {
	switch service {
	case portableCompute, portableNetworking:
		return gcpServiceCompute
	case portableStorage:
		return gcpServiceStorage
	case portableDatabase:
		return gcpServiceFirestore
	case portableServerless:
		return gcpServiceCloudFunctions
	default:
		return service + ".googleapis.com"
	}
}

func labelsOrEmpty(t map[string]string) map[string]string {
	if t == nil {
		return map[string]string{}
	}

	return t
}

func feedFromBody(body map[string]any) *feed {
	f := &feed{}
	if body == nil {
		return f
	}

	mergeFeed(f, body)

	return f
}

func mergeFeed(f *feed, body map[string]any) {
	if v, ok := body["assetNames"].([]any); ok {
		f.AssetNames = toStringSlice(v)
	}

	if v, ok := body["assetTypes"].([]any); ok {
		f.AssetTypes = toStringSlice(v)
	}

	if v, ok := body["contentType"].(string); ok {
		f.ContentType = v
	}

	if v, ok := body["labels"].(map[string]any); ok {
		f.Labels = toStringMap(v)
	}

	if v, ok := body["feedOutputConfig"].(map[string]any); ok {
		f.FeedOutput = v
	}

	if v, ok := body["condition"].(map[string]any); ok {
		f.Condition = v
	}
}

func toStringSlice(in []any) []string {
	out := make([]string, 0, len(in))

	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func toStringMap(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))

	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}

	return out
}

func feedToWire(f *feed) map[string]any {
	out := map[string]any{
		"name": f.Name,
	}

	if len(f.AssetNames) > 0 {
		out["assetNames"] = f.AssetNames
	}

	if len(f.AssetTypes) > 0 {
		out["assetTypes"] = f.AssetTypes
	}

	if f.ContentType != "" {
		out["contentType"] = f.ContentType
	}

	if f.Labels != nil {
		out["labels"] = f.Labels
	}

	if f.FeedOutput != nil {
		out["feedOutputConfig"] = f.FeedOutput
	}

	if f.Condition != nil {
		out["condition"] = f.Condition
	}

	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits a googleapi-shaped error. The status code (e.g.
// ALREADY_EXISTS) is also prefixed onto the message so callers using
// substring matching against the SDK's error.Error() string can still
// recognize it — the googleapi error helper surfaces message, not status.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	combined := code + ": " + msg
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"status":  code,
			"message": combined,
		},
	})
}

func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}

// Compile-time check the handler implements the dispatch interface.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
