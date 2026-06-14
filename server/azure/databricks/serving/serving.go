// Package serving implements the Databricks Serving Endpoints data-plane REST
// API (the /api/2.0/serving-endpoints surface served at the workspace URL) as a
// server.Handler. Point the real github.com/databricks/databricks-sdk-go
// WorkspaceClient at a server registered with a Handler from New and
// w.ServingEndpoints Create/Get/List/UpdateConfig/Delete work end-to-end
// against an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.0/serving-endpoints                create
//	GET    /api/2.0/serving-endpoints                list
//	GET    /api/2.0/serving-endpoints/{name}         get
//	PUT    /api/2.0/serving-endpoints/{name}/config  update config
//	DELETE /api/2.0/serving-endpoints/{name}         delete
//
// Create and UpdateConfig return an LRO waiter that polls Get until the
// endpoint's config_update state reaches NOT_UPDATING; this backend reports
// endpoints as terminal (config_update NOT_UPDATING, ready READY) so the waiter
// resolves on the first poll.
package serving

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

const resServingEndpoints = "serving-endpoints"

// Endpoint state values mirroring serving.EndpointStateConfigUpdate and
// serving.EndpointStateReady. A freshly created or updated endpoint is reported
// terminal so the SDK Create/UpdateConfig waiters resolve immediately.
const (
	configUpdateNotUpdating = "NOT_UPDATING"
	stateReady              = "READY"
)

// Path segment positions after splitting on "/".
const (
	idxAPI      = 0
	idxResource = 2
	idxName     = 3
	idxAction   = 4
)

// collectionSegs is the [api, ver, serving-endpoints] segment count; an item
// path adds the {name} segment for itemSegs.
const (
	collectionSegs = 3
	itemSegs       = 4
)

// actionConfig is the /{name}/config sub-route segment.
const actionConfig = "config"

// endpoint is the in-memory state for a single serving endpoint.
type endpoint struct {
	Name                 string
	ID                   string
	Description          string
	CreationTimestamp    int64
	LastUpdatedTimestamp int64
	ConfigVersion        int64
	ServedEntities       []json.RawMessage
	ServedModels         []json.RawMessage
	TrafficConfig        json.RawMessage
	Tags                 json.RawMessage
}

// Handler serves the Databricks Serving Endpoints data-plane REST API backed by
// an in-memory map keyed by endpoint name.
type Handler struct {
	mu     sync.RWMutex
	items  map[string]*endpoint
	nextID int64
}

// New returns a Serving Endpoints handler with an empty in-memory backend.
func New() *Handler {
	return &Handler{items: make(map[string]*endpoint)}
}

// Matches claims paths whose 3rd segment is "serving-endpoints".
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) >= collectionSegs && parts[idxAPI] == "api" && parts[idxResource] == resServingEndpoints
}

// ServeHTTP routes by path shape: collection (create/list), item (get/delete),
// or item action (/{name}/config).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < collectionSegs {
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unsupported path")

		return
	}

	switch len(parts) {
	case collectionSegs:
		h.serveCollection(w, r)
	case itemSegs:
		h.serveItem(w, r, parts[idxName])
	default:
		h.serveItemAction(w, r, parts[idxName], parts[idxAction])
	}
}

// serveCollection handles /api/2.0/serving-endpoints (POST create, GET list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r)
	case http.MethodGet:
		h.list(w)
	default:
		methodNotAllowed(w)
	}
}

// serveItem handles /api/2.0/serving-endpoints/{name} (GET get, DELETE delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.get(w, name)
	case http.MethodDelete:
		h.delete(w, name)
	default:
		methodNotAllowed(w)
	}
}

// serveItemAction handles /api/2.0/serving-endpoints/{name}/config.
func (h *Handler) serveItemAction(w http.ResponseWriter, r *http.Request, name, action string) {
	if action != actionConfig {
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unknown action: "+action)

		return
	}

	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	h.updateConfig(w, r, name)
}

// createRequest is the subset of CreateServingEndpoint fields we honor.
type createRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      *configInput    `json:"config"`
	Tags        json.RawMessage `json:"tags"`
}

// configInput is the subset of EndpointCoreConfigInput fields we honor.
type configInput struct {
	ServedEntities []json.RawMessage `json:"served_entities"`
	ServedModels   []json.RawMessage `json:"served_models"`
	TrafficConfig  json.RawMessage   `json:"traffic_config"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "name is required")

		return
	}

	h.mu.Lock()

	if _, ok := h.items[req.Name]; ok {
		h.mu.Unlock()
		writeErr(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS", "serving endpoint already exists: "+req.Name)

		return
	}

	h.nextID++
	ep := newEndpoint(&req, h.nextID)
	h.items[ep.Name] = ep
	out := detailed(ep)
	h.mu.Unlock()

	writeJSON(w, out)
}

// newEndpoint builds an endpoint from a create request.
func newEndpoint(req *createRequest, id int64) *endpoint {
	ep := &endpoint{
		Name:          req.Name,
		ID:            idFor(id),
		Description:   req.Description,
		ConfigVersion: 1,
		Tags:          req.Tags,
	}

	applyConfig(ep, req.Config)

	return ep
}

// applyConfig copies the served entities/models and traffic config from a
// config payload onto the endpoint.
func applyConfig(ep *endpoint, cfg *configInput) {
	if cfg == nil {
		return
	}

	ep.ServedEntities = cfg.ServedEntities
	ep.ServedModels = cfg.ServedModels
	ep.TrafficConfig = cfg.TrafficConfig
}

func (h *Handler) get(w http.ResponseWriter, name string) {
	var out map[string]any

	h.mu.RLock()
	ep, ok := h.items[name]

	if ok {
		out = detailed(ep)
	}
	h.mu.RUnlock()

	if !ok {
		notFound(w, name)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) list(w http.ResponseWriter) {
	h.mu.RLock()
	out := make([]map[string]any, 0, len(h.items))

	for _, ep := range h.items {
		out = append(out, summary(ep))
	}
	h.mu.RUnlock()

	writeJSON(w, map[string]any{"endpoints": out})
}

// updateConfigRequest is the subset of EndpointCoreConfigInput fields honored
// on the /{name}/config route. Name comes from the path, not the body.
type updateConfigRequest struct {
	ServedEntities []json.RawMessage `json:"served_entities"`
	ServedModels   []json.RawMessage `json:"served_models"`
	TrafficConfig  json.RawMessage   `json:"traffic_config"`
}

func (h *Handler) updateConfig(w http.ResponseWriter, r *http.Request, name string) {
	var req updateConfigRequest
	if !decode(w, r, &req) {
		return
	}

	var out map[string]any

	h.mu.Lock()
	ep, ok := h.items[name]

	if ok {
		ep.ConfigVersion++
		applyConfig(ep, &configInput{
			ServedEntities: req.ServedEntities,
			ServedModels:   req.ServedModels,
			TrafficConfig:  req.TrafficConfig,
		})

		out = detailed(ep)
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, name)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) delete(w http.ResponseWriter, name string) {
	h.mu.Lock()
	_, ok := h.items[name]

	if ok {
		delete(h.items, name)
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, name)

		return
	}

	writeJSON(w, struct{}{})
}

// terminalState is the always-terminal EndpointState reported for every
// endpoint, letting Create/UpdateConfig waiters resolve on the first poll.
func terminalState() map[string]any {
	return map[string]any{
		"config_update": configUpdateNotUpdating,
		"ready":         stateReady,
	}
}

// configOutput renders an endpoint's current config as the
// EndpointCoreConfigOutput JSON shape used by Get/Create/UpdateConfig.
func configOutput(ep *endpoint) map[string]any {
	out := map[string]any{"config_version": ep.ConfigVersion}

	if ep.ServedEntities != nil {
		out["served_entities"] = ep.ServedEntities
	}

	if ep.ServedModels != nil {
		out["served_models"] = ep.ServedModels
	}

	if ep.TrafficConfig != nil {
		out["traffic_config"] = ep.TrafficConfig
	}

	return out
}

// detailed renders an endpoint as the ServingEndpointDetailed JSON shape
// returned by Get, Create, and UpdateConfig.
func detailed(ep *endpoint) map[string]any {
	out := map[string]any{
		"name":                   ep.Name,
		"id":                     ep.ID,
		"creation_timestamp":     ep.CreationTimestamp,
		"last_updated_timestamp": ep.LastUpdatedTimestamp,
		"state":                  terminalState(),
		"config":                 configOutput(ep),
	}

	if ep.Description != "" {
		out["description"] = ep.Description
	}

	if ep.Tags != nil {
		out["tags"] = ep.Tags
	}

	return out
}

// summary renders an endpoint as the ServingEndpoint JSON shape returned by
// List, whose config is an EndpointCoreConfigSummary.
func summary(ep *endpoint) map[string]any {
	cfg := map[string]any{}

	if ep.ServedEntities != nil {
		cfg["served_entities"] = ep.ServedEntities
	}

	if ep.ServedModels != nil {
		cfg["served_models"] = ep.ServedModels
	}

	out := map[string]any{
		"name":                   ep.Name,
		"id":                     ep.ID,
		"creation_timestamp":     ep.CreationTimestamp,
		"last_updated_timestamp": ep.LastUpdatedTimestamp,
		"state":                  terminalState(),
		"config":                 cfg,
	}

	if ep.Description != "" {
		out["description"] = ep.Description
	}

	if ep.Tags != nil {
		out["tags"] = ep.Tags
	}

	return out
}

// idFor builds a stable system-generated endpoint id.
func idFor(seq int64) string {
	return "endpoint-" + itoa(seq)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}

	var buf [20]byte
	pos := len(buf)

	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}

	return string(buf[pos:])
}

// splitPath strips the leading/trailing "/" and returns the path segments,
// keeping "api" at index 0 for the Matches/ServeHTTP guards.
func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the Databricks error envelope shape.
type errorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}

func notFound(w http.ResponseWriter, name string) {
	writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "serving endpoint not found: "+name)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
