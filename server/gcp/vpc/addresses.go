package vpc

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Addresses are reserved IP ranges. Private services access uses a global one
// to carve out the block a managed service is peered into, so a caller
// reserves it while building a network and releases it while tearing one
// down — which is where its absence stops the work.
//
// Like routers, these are held in the handler rather than the networking
// driver: a reserved range with a purpose and prefix length is specific to
// this provider's shape, not part of the portable subset.
type addressStore struct {
	mu        sync.RWMutex
	addresses map[string]map[string]json.RawMessage // project/scope -> name -> body
}

func newAddressStore() *addressStore {
	return &addressStore{addresses: map[string]map[string]json.RawMessage{}}
}

func (s *addressStore) key(project, scope string) string { return project + "/" + scope }

func (s *addressStore) put(project, scope, name string, body json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.key(project, scope)
	if s.addresses[k] == nil {
		s.addresses[k] = map[string]json.RawMessage{}
	}

	s.addresses[k][name] = body
}

func (s *addressStore) get(project, scope, name string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.addresses[s.key(project, scope)][name]

	return b, ok
}

func (s *addressStore) list(project, scope string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byName := s.addresses[s.key(project, scope)]
	out := make([]json.RawMessage, 0, len(byName))

	for _, b := range byName {
		out = append(out, b)
	}

	return out
}

func (s *addressStore) delete(project, scope, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.key(project, scope)
	if _, ok := s.addresses[k][name]; !ok {
		return false
	}

	delete(s.addresses[k], name)

	return true
}

// scopeOf keys an address by the scope it was reserved in, so a global
// address and a regional one of the same name stay distinct.
//
//nolint:gocritic // rp is a request-scoped value
func scopeOf(rp gcprest.ResourcePath) string {
	if rp.Scope == gcprest.ScopeGlobal {
		return gcprest.ScopeGlobal
	}

	return rp.ScopeName
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeAddresses(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertAddress(w, r, rp)
		case http.MethodGet:
			h.listAddresses(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAddress(w, r, rp)
	case http.MethodDelete:
		h.deleteAddress(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var raw json.RawMessage
	if !gcprest.DecodeJSON(w, r, &raw) {
		return
	}

	var named struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(raw, &named); err != nil || named.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name is required")
		return
	}

	if _, exists := h.addresses.get(rp.Project, scopeOf(rp), named.Name); exists {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"address "+named.Name+" already exists")

		return
	}

	h.addresses.put(rp.Project, scopeOf(rp), named.Name, raw)

	gcprest.WriteJSON(w, http.StatusOK, gcprest.NewDoneOperation(hostOf(r), rp.Project,
		rp.Scope, rp.ScopeName, resourceAddresses, named.Name, "insert"))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	body, ok := h.addresses.get(rp.Project, scopeOf(rp), rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"address "+rp.ResourceName+" not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":  "compute#addressList",
		"items": h.addresses.list(rp.Project, scopeOf(rp)),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if !h.addresses.delete(rp.Project, scopeOf(rp), rp.ResourceName) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"address "+rp.ResourceName+" not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, gcprest.NewDoneOperation(hostOf(r), rp.Project,
		rp.Scope, rp.ScopeName, resourceAddresses, rp.ResourceName, "delete"))
}
