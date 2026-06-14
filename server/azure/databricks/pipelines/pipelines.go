// Package pipelines implements the Databricks Pipelines (Delta Live Tables)
// data-plane REST API (the /api/2.0/pipelines surface) as a server.Handler.
// Point the real github.com/databricks/databricks-sdk-go WorkspaceClient at a
// server registered with a Handler from New and w.Pipelines works end-to-end
// against an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.0/pipelines
//	GET    /api/2.0/pipelines
//	GET    /api/2.0/pipelines/{id}
//	PUT    /api/2.0/pipelines/{id}
//	DELETE /api/2.0/pipelines/{id}
//	POST   /api/2.0/pipelines/{id}/updates
//	POST   /api/2.0/pipelines/{id}/stop
package pipelines

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

// Pipeline lifecycle state. Pipelines settle in IDLE so the SDK Stop waiter,
// which polls Get until IDLE, resolves immediately. The STARTING/RUNNING/
// STOPPING intermediate states are not emitted by this in-memory backend.
const stateIdle = "IDLE"

// Path segment positions after splitting on "/" ([api, ver, pipelines, id, act]).
const (
	idxAPI      = 0
	idxResource = 2
	idxID       = 3
	idxAction   = 4
)

// minMatchSegs is the [api, ver, pipelines] segment count Matches needs.
const minMatchSegs = 3

const actionUpdates = "updates"
const actionStop = "stop"

// pipeline is the in-memory state for a single Delta Live Tables pipeline.
type pipeline struct {
	ID           string
	Name         string
	Storage      string
	Target       string
	Catalog      string
	Channel      string
	Edition      string
	Continuous   bool
	Development  bool
	State        string
	CreatorName  string
	LatestUpdate string
}

// Handler serves the Databricks Pipelines data-plane REST API.
type Handler struct {
	mu       sync.RWMutex
	items    map[string]*pipeline
	nextID   int
	updateID int
}

// New returns a Pipelines handler backed by an empty in-memory store.
func New() *Handler {
	return &Handler{items: make(map[string]*pipeline)}
}

// Matches claims /api/{ver}/pipelines/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) < minMatchSegs || parts[idxAPI] != "api" {
		return false
	}

	return parts[idxResource] == "pipelines"
}

// ServeHTTP routes by path shape: collection (create/list) vs. item (get/
// update/delete) vs. item action (updates/stop).
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

// serveCollection handles /api/2.0/pipelines (POST create, GET list).
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

// serveItem handles /api/2.0/pipelines/{id}[/{action}].
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[idxID]

	if len(parts) > idxAction {
		h.serveItemAction(w, r, id, parts[idxAction])

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.get(w, id)
	case http.MethodPut:
		h.update(w, r, id)
	case http.MethodDelete:
		h.delete(w, id)
	default:
		methodNotAllowed(w)
	}
}

// serveItemAction handles the /{id}/{updates,stop} sub-routes.
func (h *Handler) serveItemAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch action {
	case actionUpdates:
		h.startUpdate(w, id)
	case actionStop:
		h.stop(w, id)
	default:
		writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "unknown action: "+action)
	}
}

// createRequest is the subset of CreatePipeline fields we honor.
type createRequest struct {
	Name        string `json:"name"`
	Storage     string `json:"storage"`
	Target      string `json:"target"`
	Catalog     string `json:"catalog"`
	Channel     string `json:"channel"`
	Edition     string `json:"edition"`
	Continuous  bool   `json:"continuous"`
	Development bool   `json:"development"`
	CreatorName string `json:"creator_user_name"`
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

	p := newPipeline(&req)

	h.mu.Lock()
	h.nextID++
	p.ID = fmt.Sprintf("pipe-%d", h.nextID)
	h.items[p.ID] = p
	h.mu.Unlock()

	writeJSON(w, map[string]string{"pipeline_id": p.ID})
}

// newPipeline builds a pipeline from a create request. Pipelines start IDLE so
// any state-polling waiter resolves immediately.
func newPipeline(req *createRequest) *pipeline {
	return &pipeline{
		Name:        req.Name,
		Storage:     req.Storage,
		Target:      req.Target,
		Catalog:     req.Catalog,
		Channel:     req.Channel,
		Edition:     req.Edition,
		Continuous:  req.Continuous,
		Development: req.Development,
		State:       stateIdle,
		CreatorName: req.CreatorName,
	}
}

func (h *Handler) get(w http.ResponseWriter, id string) {
	var snapshot pipeline

	h.mu.RLock()
	p, ok := h.items[id]

	if ok {
		snapshot = *p
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

	for _, p := range h.items {
		out = append(out, toStateInfo(p))
	}
	h.mu.RUnlock()

	writeJSON(w, map[string]any{"statuses": out})
}

// updateRequest is the subset of EditPipeline fields we honor.
type updateRequest struct {
	Name        string `json:"name"`
	Storage     string `json:"storage"`
	Target      string `json:"target"`
	Catalog     string `json:"catalog"`
	Channel     string `json:"channel"`
	Edition     string `json:"edition"`
	Continuous  bool   `json:"continuous"`
	Development bool   `json:"development"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, id string) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()
	p, ok := h.items[id]

	if ok {
		applyUpdate(p, &req)
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

func applyUpdate(p *pipeline, req *updateRequest) {
	if req.Name != "" {
		p.Name = req.Name
	}

	if req.Storage != "" {
		p.Storage = req.Storage
	}

	if req.Target != "" {
		p.Target = req.Target
	}

	if req.Catalog != "" {
		p.Catalog = req.Catalog
	}

	if req.Channel != "" {
		p.Channel = req.Channel
	}

	if req.Edition != "" {
		p.Edition = req.Edition
	}

	p.Continuous = req.Continuous
	p.Development = req.Development
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

func (h *Handler) startUpdate(w http.ResponseWriter, id string) {
	var updateID string

	h.mu.Lock()
	p, ok := h.items[id]

	if ok {
		h.updateID++
		updateID = fmt.Sprintf("upd-%d", h.updateID)
		p.LatestUpdate = updateID
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, map[string]string{"update_id": updateID})
}

// stop drives a pipeline back to IDLE. The SDK Stop call returns a waiter that
// polls Get until IDLE, so leaving the pipeline IDLE lets the waiter resolve.
func (h *Handler) stop(w http.ResponseWriter, id string) {
	h.mu.Lock()
	p, ok := h.items[id]

	if ok {
		p.State = stateIdle
	}
	h.mu.Unlock()

	if !ok {
		notFound(w, id)

		return
	}

	writeJSON(w, struct{}{})
}

// toResponse renders a pipeline as the GetPipelineResponse JSON shape.
func toResponse(p *pipeline) map[string]any {
	return map[string]any{
		"pipeline_id":       p.ID,
		"name":              p.Name,
		"state":             p.State,
		"creator_user_name": p.CreatorName,
		"spec":              toSpec(p),
	}
}

// toSpec renders the pipeline settings as the PipelineSpec JSON shape.
func toSpec(p *pipeline) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"storage":     p.Storage,
		"target":      p.Target,
		"catalog":     p.Catalog,
		"channel":     p.Channel,
		"edition":     p.Edition,
		"continuous":  p.Continuous,
		"development": p.Development,
	}
}

// toStateInfo renders a pipeline as the PipelineStateInfo JSON shape used by
// ListPipelines.
func toStateInfo(p *pipeline) map[string]any {
	return map[string]any{
		"pipeline_id":       p.ID,
		"name":              p.Name,
		"state":             p.State,
		"creator_user_name": p.CreatorName,
	}
}

// splitPath strips the leading/trailing "/" and returns the path segments.
func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
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
	writeErr(w, http.StatusNotFound, "RESOURCE_DOES_NOT_EXIST", "pipeline not found: "+id)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}
