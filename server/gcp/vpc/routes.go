package vpc

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Routes are custom static routes on a VPC network — the record that sends a
// destination range to a next hop (gateway, IP, or instance). A caller
// building a network with an internet or NAT route creates one here, so
// without the collection the network step stops before egress works.
//
// Like routers and addresses, routes are held in the handler rather than the
// networking driver: a route's next-hop shape is specific to this provider's
// REST surface, not part of the portable subset. GCP routes are global.
type routeStore struct {
	mu     sync.RWMutex
	routes map[string]map[string]json.RawMessage // project -> name -> body
}

func newRouteStore() *routeStore {
	return &routeStore{routes: map[string]map[string]json.RawMessage{}}
}

func (s *routeStore) put(project, name string, body json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.routes[project] == nil {
		s.routes[project] = map[string]json.RawMessage{}
	}

	s.routes[project][name] = body
}

func (s *routeStore) get(project, name string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.routes[project][name]

	return b, ok
}

func (s *routeStore) list(project string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byName := s.routes[project]
	out := make([]json.RawMessage, 0, len(byName))

	for _, b := range byName {
		out = append(out, b)
	}

	return out
}

func (s *routeStore) delete(project, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.routes[project][name]; !ok {
		return false
	}

	delete(s.routes[project], name)

	return true
}

//nolint:gocritic,dupl // rp is a request-scoped value; CRUD route shape is duplicate-by-design across resource types
func (h *Handler) routeRoutes(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertRoute(w, r, rp)
		case http.MethodGet:
			h.listRoutes(w, r, rp)
		default:
			gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRoute(w, r, rp)
	case http.MethodDelete:
		h.deleteRoute(w, r, rp)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "routes are global")
		return
	}

	var raw json.RawMessage
	if !gcprest.DecodeJSON(w, r, &raw) {
		return
	}

	name := rawName(raw)
	if name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "name is required")
		return
	}

	if _, exists := h.routes.get(rp.Project, name); exists {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists", "route "+name+" already exists")
		return
	}

	h.routes.put(rp.Project, name, enrichRoute(raw, rp, hostOf(r), name))

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project,
		gcprest.ScopeGlobal, "", resourceRoutes, name, "insert"))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getRoute(w http.ResponseWriter, _ *http.Request, rp gcprest.ResourcePath) {
	body, ok := h.routes.get(rp.Project, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "route "+rp.ResourceName+" not found")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, body)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	all := h.routes.list(rp.Project)
	filter := r.URL.Query().Get("filter")

	items := make([]json.RawMessage, 0, len(all))

	for _, body := range all {
		if nameMatches(filter, rawName(body)) {
			items = append(items, body)
		}
	}

	sort.SliceStable(items, func(i, j int) bool { return rawName(items[i]) < rawName(items[j]) })

	page, err := pagination.Paginate(items, r.URL.Query().Get("pageToken"),
		maxResultsOf(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := map[string]any{
		"kind":     "compute#routeList",
		"id":       "projects/" + rp.Project + "/global/routes",
		"items":    page.Items,
		"selfLink": gcprest.SelfLink(hostOf(r), rp.Project, gcprest.ScopeGlobal, "", resourceRoutes, ""),
	}
	if page.NextPageToken != "" {
		out["nextPageToken"] = page.NextPageToken
	}

	gcprest.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if !h.routes.delete(rp.Project, rp.ResourceName) {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "route "+rp.ResourceName+" not found")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project,
		gcprest.ScopeGlobal, "", resourceRoutes, rp.ResourceName, "delete"))
}

// enrichRoute stamps the server-assigned fields (kind, id, selfLink,
// creationTimestamp) onto a route while preserving the caller's routing spec
// (destRange, priority). It also normalizes the reference fields — network and
// the global next-hop references — to fully-qualified self-link URLs, matching
// real GCP's read shape so a caller (Terraform google_compute_route) that sends
// a relative "global/networks/default" doesn't read back a value that never
// stops diffing against the API's absolute self-link. Without this a Get returns
// the caller's raw relative reference verbatim.
//
//nolint:gocritic // rp is a request-scoped value
func enrichRoute(raw json.RawMessage, rp gcprest.ResourcePath, host, name string) json.RawMessage {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return raw
	}

	body["kind"] = "compute#route"
	body["id"] = numericID(rp.Project + "/routes/" + name)
	body["selfLink"] = gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceRoutes, name)
	body["creationTimestamp"] = nowRFC3339()

	qualifyGlobalRef(body, "network", host, rp.Project, "networks")
	qualifyGlobalRef(body, "nextHopNetwork", host, rp.Project, "networks")
	qualifyGlobalRef(body, "nextHopGateway", host, rp.Project, "gateways")

	enriched, err := json.Marshal(body)
	if err != nil {
		return raw
	}

	return enriched
}

// qualifyGlobalRef rewrites body[field], when present and non-empty, to a
// fully-qualified global self-link under collection (e.g. networks, gateways),
// preserving only the trailing name segment of whatever reference the caller
// supplied (bare name, relative path, or absolute URL). A field that is absent
// or not a string is left untouched.
func qualifyGlobalRef(body map[string]any, field, host, project, collection string) {
	raw, ok := body[field].(string)
	if !ok || raw == "" {
		return
	}

	body[field] = gcprest.SelfLink(host, project, gcprest.ScopeGlobal, "", collection, lastSegment(raw))
}
