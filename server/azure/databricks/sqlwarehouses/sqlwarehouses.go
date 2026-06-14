// Package sqlwarehouses implements the Databricks SQL Warehouses data-plane
// REST API (the /api/2.0/sql/warehouses surface) as a server.Handler. Point the
// real github.com/databricks/databricks-sdk-go WorkspaceClient at a server
// registered with a Handler from New and w.Warehouses works end-to-end against
// an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.0/sql/warehouses
//	GET    /api/2.0/sql/warehouses
//	GET    /api/2.0/sql/warehouses/{id}
//	POST   /api/2.0/sql/warehouses/{id}/edit
//	DELETE /api/2.0/sql/warehouses/{id}
//	POST   /api/2.0/sql/warehouses/{id}/start
//	POST   /api/2.0/sql/warehouses/{id}/stop
package sqlwarehouses

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

// Warehouse lifecycle states, mirroring sql.State. Create/Start drive a
// warehouse to RUNNING and Stop to STOPPED; the STARTING/STOPPING/DELETING/
// DELETED intermediate states are not emitted by this in-memory backend.
const (
	stateRunning = "RUNNING"
	stateStopped = "STOPPED"
)

// Defaults applied when a create request omits the field.
const (
	defaultAutoStopMins   = 120
	defaultClusterSize    = "X-Small"
	defaultMaxNumClusters = 1
	defaultMinNumClusters = 1
)

// Path segment positions after splitting on "/".
const (
	idxAPI       = 0
	idxSQL       = 2
	idxWarehouse = 3
	idxID        = 4
	idxAction    = 5
)

// minMatchSegs is the [api, ver, sql, warehouses] segment count Matches needs.
const minMatchSegs = 4

// warehouse is the in-memory state for a single SQL warehouse.
type warehouse struct {
	ID                      string
	Name                    string
	ClusterSize             string
	State                   string
	AutoStopMins            int
	MaxNumClusters          int
	MinNumClusters          int
	NumClusters             int
	EnablePhoton            bool
	EnableServerlessCompute bool
	CreatorName             string
	WarehouseType           string
	SpotInstancePolicy      string
	Tags                    map[string]string
}

// endpointTagPair / endpointTags mirror sql.EndpointTagPair / sql.EndpointTags
// — the wire shape of a warehouse's "tags" object.
type endpointTagPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type endpointTags struct {
	CustomTags []endpointTagPair `json:"custom_tags"`
}

func (t *endpointTags) toMap() map[string]string {
	if t == nil || len(t.CustomTags) == 0 {
		return nil
	}

	out := make(map[string]string, len(t.CustomTags))
	for _, p := range t.CustomTags {
		out[p.Key] = p.Value
	}

	return out
}

// tagsToWire renders a tag map back into the {custom_tags: [{key,value}]} wire
// shape, with keys sorted so the output is deterministic.
func tagsToWire(tags map[string]string) *endpointTags {
	if len(tags) == 0 {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	pairs := make([]endpointTagPair, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, endpointTagPair{Key: k, Value: tags[k]})
	}

	return &endpointTags{CustomTags: pairs}
}

// Handler serves the Databricks SQL Warehouses data-plane REST API.
type Handler struct {
	mu     sync.RWMutex
	items  map[string]*warehouse
	nextID int
}

// New returns a SQL Warehouses handler backed by an empty in-memory store.
func New() *Handler {
	return &Handler{items: make(map[string]*warehouse)}
}

// Matches claims /api/{ver}/sql/warehouses/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) < minMatchSegs || parts[idxAPI] != "api" {
		return false
	}

	return parts[idxSQL] == "sql" && parts[idxWarehouse] == "warehouses"
}

// ServeHTTP routes by path shape: collection (create/list) vs. item (get/edit/
// delete/start/stop).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < minMatchSegs {
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unsupported path")

		return
	}

	if len(parts) == minMatchSegs {
		h.serveCollection(w, r)

		return
	}

	h.serveItem(w, r, parts)
}

// serveCollection handles /api/2.0/sql/warehouses (POST create, GET list).
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

// serveItem handles /api/2.0/sql/warehouses/{id}[/{action}].
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[idxID]

	if len(parts) > idxAction {
		h.serveItemAction(w, r, id, parts[idxAction])

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.get(w, id)
	case http.MethodDelete:
		h.delete(w, id)
	default:
		methodNotAllowed(w)
	}
}

// serveItemAction handles the /{id}/{edit,start,stop} sub-routes.
func (h *Handler) serveItemAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch action {
	case "edit":
		h.edit(w, r, id)
	case "start":
		h.start(w, id)
	case "stop":
		h.stop(w, id)
	default:
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unknown action: "+action)
	}
}

// createRequest is the subset of CreateWarehouseRequest fields we honor.
// AutoStopMins is a pointer so an explicit 0 (disable auto-stop) is
// distinguishable from an omitted field (which gets the default).
type createRequest struct {
	Name                    string        `json:"name"`
	ClusterSize             string        `json:"cluster_size"`
	AutoStopMins            *int          `json:"auto_stop_mins"`
	MaxNumClusters          int           `json:"max_num_clusters"`
	MinNumClusters          int           `json:"min_num_clusters"`
	EnablePhoton            bool          `json:"enable_photon"`
	EnableServerlessCompute bool          `json:"enable_serverless_compute"`
	CreatorName             string        `json:"creator_name"`
	WarehouseType           string        `json:"warehouse_type"`
	SpotInstancePolicy      string        `json:"spot_instance_policy"`
	Tags                    *endpointTags `json:"tags"`
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

	wh := newWarehouse(&req)

	h.mu.Lock()
	h.nextID++
	wh.ID = fmt.Sprintf("%d", h.nextID)
	h.items[wh.ID] = wh
	h.mu.Unlock()

	writeJSON(w, map[string]string{"id": wh.ID})
}

// newWarehouse builds a warehouse from a create request, applying defaults. The
// Create SDK call returns an LRO waiter that polls Get until RUNNING, so a fresh
// warehouse starts RUNNING for the waiter to resolve immediately.
func newWarehouse(req *createRequest) *warehouse {
	wh := &warehouse{
		Name:                    req.Name,
		ClusterSize:             req.ClusterSize,
		State:                   stateRunning,
		MaxNumClusters:          req.MaxNumClusters,
		MinNumClusters:          req.MinNumClusters,
		EnablePhoton:            req.EnablePhoton,
		EnableServerlessCompute: req.EnableServerlessCompute,
		CreatorName:             req.CreatorName,
		WarehouseType:           req.WarehouseType,
		SpotInstancePolicy:      req.SpotInstancePolicy,
		Tags:                    req.Tags.toMap(),
	}

	if wh.ClusterSize == "" {
		wh.ClusterSize = defaultClusterSize
	}

	// A nil AutoStopMins means the field was omitted — apply the default. An
	// explicit value (including 0, which disables auto-stop) is honored as-is.
	if req.AutoStopMins == nil {
		wh.AutoStopMins = defaultAutoStopMins
	} else {
		wh.AutoStopMins = *req.AutoStopMins
	}

	if wh.MaxNumClusters == 0 {
		wh.MaxNumClusters = defaultMaxNumClusters
	}

	if wh.MinNumClusters == 0 {
		wh.MinNumClusters = defaultMinNumClusters
	}

	wh.NumClusters = wh.MinNumClusters

	return wh
}

func (h *Handler) get(w http.ResponseWriter, id string) {
	var snapshot warehouse

	h.mu.RLock()
	wh, ok := h.items[id]

	if ok {
		snapshot = *wh
	}
	h.mu.RUnlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, toResponse(&snapshot))
}

func (h *Handler) list(w http.ResponseWriter) {
	h.mu.RLock()
	out := make([]map[string]any, 0, len(h.items))

	for _, wh := range h.items {
		out = append(out, toResponse(wh))
	}
	h.mu.RUnlock()

	writeJSON(w, map[string]any{"warehouses": out})
}

// editRequest is the subset of EditWarehouseRequest fields we honor.
// AutoStopMins is a pointer for the same explicit-0 reason as createRequest.
type editRequest struct {
	Name           string        `json:"name"`
	ClusterSize    string        `json:"cluster_size"`
	AutoStopMins   *int          `json:"auto_stop_mins"`
	MaxNumClusters int           `json:"max_num_clusters"`
	MinNumClusters int           `json:"min_num_clusters"`
	Tags           *endpointTags `json:"tags"`
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request, id string) {
	var req editRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()
	wh, ok := h.items[id]

	if ok {
		applyEdit(wh, req)
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

func applyEdit(wh *warehouse, req editRequest) {
	if req.Name != "" {
		wh.Name = req.Name
	}

	if req.ClusterSize != "" {
		wh.ClusterSize = req.ClusterSize
	}

	if req.AutoStopMins != nil {
		wh.AutoStopMins = *req.AutoStopMins
	}

	if req.MaxNumClusters != 0 {
		wh.MaxNumClusters = req.MaxNumClusters
	}

	if req.MinNumClusters != 0 {
		wh.MinNumClusters = req.MinNumClusters
	}

	if tags := req.Tags.toMap(); tags != nil {
		wh.Tags = tags
	}
}

func (h *Handler) delete(w http.ResponseWriter, id string) {
	h.mu.Lock()
	_, ok := h.items[id]

	if ok {
		delete(h.items, id)
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) start(w http.ResponseWriter, id string) {
	h.transition(w, id, stateRunning)
}

func (h *Handler) stop(w http.ResponseWriter, id string) {
	h.transition(w, id, stateStopped)
}

// transition sets a warehouse to the given terminal state. The Start/Stop SDK
// calls return LRO waiters that poll Get until RUNNING/STOPPED respectively, so
// setting the terminal state directly lets those waiters resolve.
func (h *Handler) transition(w http.ResponseWriter, id, state string) {
	h.mu.Lock()
	wh, ok := h.items[id]

	if ok {
		wh.State = state
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

// toResponse renders a warehouse as the GetWarehouseResponse / EndpointInfo JSON
// shape shared by Get and List.
func toResponse(wh *warehouse) map[string]any {
	out := map[string]any{
		"id":                        wh.ID,
		"name":                      wh.Name,
		"cluster_size":              wh.ClusterSize,
		"state":                     wh.State,
		"auto_stop_mins":            wh.AutoStopMins,
		"max_num_clusters":          wh.MaxNumClusters,
		"min_num_clusters":          wh.MinNumClusters,
		"num_clusters":              wh.NumClusters,
		"enable_photon":             wh.EnablePhoton,
		"enable_serverless_compute": wh.EnableServerlessCompute,
		"creator_name":              wh.CreatorName,
		"warehouse_type":            wh.WarehouseType,
		"spot_instance_policy":      wh.SpotInstancePolicy,
	}

	if tags := tagsToWire(wh.Tags); tags != nil {
		out["tags"] = tags
	}

	return out
}

// splitPath strips the leading/trailing "/" and returns the path segments.
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

func notFound(w http.ResponseWriter, id string) {
	writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "warehouse not found: "+id)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
