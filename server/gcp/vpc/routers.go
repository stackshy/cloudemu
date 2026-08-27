package vpc

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Cloud Routers carry the NAT configuration that gives instances on a private
// subnet outbound access. A caller building a private network creates the
// router, then patches NAT onto it, so without this the network step stops
// after the subnets exist and before anything on them can reach out.
//
// Routers are held here rather than in the networking driver: the driver
// models the portable subset shared across clouds, and a router with embedded
// NAT blocks is specific to this provider's REST shape.
type routerStore struct {
	mu      sync.RWMutex
	routers map[string]map[string]json.RawMessage // project/region -> name -> body
}

func newRouterStore() *routerStore {
	return &routerStore{routers: map[string]map[string]json.RawMessage{}}
}

func (s *routerStore) scope(project, region string) string {
	return project + "/" + region
}

func (s *routerStore) put(project, region, name string, body json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.scope(project, region)
	if s.routers[k] == nil {
		s.routers[k] = map[string]json.RawMessage{}
	}

	s.routers[k][name] = body
}

func (s *routerStore) get(project, region, name string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	body, ok := s.routers[s.scope(project, region)][name]

	return body, ok
}

func (s *routerStore) list(project, region string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byName := s.routers[s.scope(project, region)]
	out := make([]json.RawMessage, 0, len(byName))

	for _, body := range byName {
		out = append(out, body)
	}

	return out
}

func (s *routerStore) delete(project, region, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.scope(project, region)
	if _, ok := s.routers[k][name]; !ok {
		return false
	}

	delete(s.routers[k], name)

	return true
}

//nolint:gocritic // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeRouters(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertRouter(w, r, rp)
		case http.MethodGet:
			h.listRouters(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRouter(w, r, rp)
	case http.MethodPatch, http.MethodPut:
		h.patchRouter(w, r, rp)
	case http.MethodDelete:
		h.deleteRouter(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// routerBody is the slice of a router this handler needs to read. Everything
// else the caller sends — NAT blocks, BGP settings — is stored verbatim and
// echoed back, so a caller that patches an unmodelled field still reads it.
type routerBody struct {
	Name string `json:"name"`
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertRouter(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	body, req, ok := decodeRouter(w, r)
	if !ok {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name is required")
		return
	}

	if _, exists := h.routers.get(rp.Project, rp.ScopeName, req.Name); exists {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"router "+req.Name+" already exists")

		return
	}

	h.routers.put(rp.Project, rp.ScopeName, req.Name, body)

	op := h.ops.RecordDone(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		resourceRouters, req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getRouter(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	body, ok := h.routers.get(rp.Project, rp.ScopeName, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"router "+rp.ResourceName+" not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listRouters(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":  "compute#routerList",
		"items": h.routers.list(rp.Project, rp.ScopeName),
	})
}

// patchRouter replaces the stored body.
//
// Real Compute merges the patch into the resource, but a caller adding NAT
// sends the whole router back, and storing what it sent is what makes the
// subsequent read agree with it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchRouter(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if _, ok := h.routers.get(rp.Project, rp.ScopeName, rp.ResourceName); !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"router "+rp.ResourceName+" not found")

		return
	}

	body, _, ok := decodeRouter(w, r)
	if !ok {
		return
	}

	h.routers.put(rp.Project, rp.ScopeName, rp.ResourceName, body)

	op := h.ops.RecordDone(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		resourceRouters, rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteRouter(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if !h.routers.delete(rp.Project, rp.ScopeName, rp.ResourceName) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"router "+rp.ResourceName+" not found")

		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, gcprest.ScopeRegions, rp.ScopeName,
		resourceRouters, rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// decodeRouter reads the request body once, returning both the raw bytes to
// store and the fields this handler acts on.
func decodeRouter(w http.ResponseWriter, r *http.Request) (json.RawMessage, routerBody, bool) {
	var raw json.RawMessage
	if !gcprest.DecodeJSON(w, r, &raw) {
		return nil, routerBody{}, false
	}

	var parsed routerBody
	if err := json.Unmarshal(raw, &parsed); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed body")

		return nil, routerBody{}, false
	}

	return raw, parsed, true
}
