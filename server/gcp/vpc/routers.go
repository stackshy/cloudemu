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

	h.routers.put(rp.Project, rp.ScopeName, req.Name,
		enrichRouter(body, rp, hostOf(r), req.Name, nil))

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

// patchRouter merges the patch into the stored router, matching real Compute's
// field-level PATCH semantics.
//
// Terraform's google_compute_router_nat adds NAT with a partial patch that
// carries only {nats:[...]} — no name, network, or bgp — so replacing the
// stored body would drop those and make the next google_compute_router read
// diff (a forced replacement). Merging top-level fields, patch wins, keeps the
// router's other settings while the caller's nats array replaces the old one.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchRouter(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	prior, ok := h.routers.get(rp.Project, rp.ScopeName, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"router "+rp.ResourceName+" not found")

		return
	}

	body, _, ok := decodeRouter(w, r)
	if !ok {
		return
	}

	h.routers.put(rp.Project, rp.ScopeName, rp.ResourceName,
		enrichRouter(mergeRouterPatch(prior, body), rp, hostOf(r), rp.ResourceName, prior))

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

// NAT idle-timeout defaults real Compute stamps on every nat block that omits
// them. A caller (Terraform's google_compute_router_nat, gcloud) reads these
// back, so filling them is what stops a create from perpetually diffing against
// an empty read.
const (
	natUDPIdleTimeoutDefault            = 30
	natTCPEstablishedIdleTimeoutDefault = 1200
	natTCPTransitoryIdleTimeoutDefault  = 30
	natICMPIdleTimeoutDefault           = 30
)

// mergeRouterPatch overlays the caller's patch onto the prior stored body at
// the top level (patch fields win), reproducing real Compute's partial-PATCH
// merge so a patch that omits a field leaves the stored value intact. A nats or
// bgp block in the patch replaces the prior one wholesale, matching the API. If
// either side is unparseable the patch is returned unchanged (replace).
func mergeRouterPatch(prior, patch json.RawMessage) json.RawMessage {
	var base, over map[string]any
	if err := json.Unmarshal(prior, &base); err != nil || base == nil {
		return patch
	}

	if err := json.Unmarshal(patch, &over); err != nil || over == nil {
		return patch
	}

	for k, v := range over {
		base[k] = v
	}

	merged, err := json.Marshal(base)
	if err != nil {
		return patch
	}

	return merged
}

// enrichRouter stamps the server-assigned fields (kind, id, selfLink, region,
// creationTimestamp) real Compute returns on a router, and fills the NAT
// idle-timeout defaults on each nat block, while preserving everything the
// caller sent (bgp, nats, interfaces). Without selfLink a Get returns a body
// the Terraform google_compute_router provider dereferences unconditionally,
// crashing its Read; without the timeout defaults a google_compute_router_nat
// create never stops diffing. On patch, prior carries the stored body so the
// original creationTimestamp survives a read-modify-write (adding a NAT).
//
//nolint:gocritic // rp is a request-scoped value
func enrichRouter(raw json.RawMessage, rp gcprest.ResourcePath, host, name string, prior json.RawMessage) json.RawMessage {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return raw
	}

	body["kind"] = "compute#router"
	body["id"] = numericID(rp.Project + "/" + rp.ScopeName + "/routers/" + name)
	body["selfLink"] = gcprest.SelfLink(host, rp.Project, gcprest.ScopeRegions, rp.ScopeName, resourceRouters, name)
	body["region"] = host + "/compute/v1/projects/" + rp.Project + "/regions/" + rp.ScopeName
	body["creationTimestamp"] = routerCreationTimestamp(body, prior)

	qualifyGlobalRef(body, "network", host, rp.Project, "networks")
	applyNatDefaults(body)

	enriched, err := json.Marshal(body)
	if err != nil {
		return raw
	}

	return enriched
}

// routerCreationTimestamp keeps the creationTimestamp stable across a patch:
// the caller's read-modify-write echoes the value we returned, and any stored
// prior wins over it, so only a first insert stamps a fresh time.
func routerCreationTimestamp(body map[string]any, prior json.RawMessage) string {
	if prior != nil {
		var p map[string]any
		if err := json.Unmarshal(prior, &p); err == nil {
			if ts, ok := p["creationTimestamp"].(string); ok && ts != "" {
				return ts
			}
		}
	}

	if ts, ok := body["creationTimestamp"].(string); ok && ts != "" {
		return ts
	}

	return nowRFC3339()
}

// applyNatDefaults fills the idle-timeout fields real Compute defaults on each
// nat block that omits them, leaving any caller-supplied value untouched.
func applyNatDefaults(body map[string]any) {
	nats, ok := body["nats"].([]any)
	if !ok {
		return
	}

	for _, n := range nats {
		nat, ok := n.(map[string]any)
		if !ok {
			continue
		}

		setDefaultInt(nat, "udpIdleTimeoutSec", natUDPIdleTimeoutDefault)
		setDefaultInt(nat, "tcpEstablishedIdleTimeoutSec", natTCPEstablishedIdleTimeoutDefault)
		setDefaultInt(nat, "tcpTransitoryIdleTimeoutSec", natTCPTransitoryIdleTimeoutDefault)
		setDefaultInt(nat, "icmpIdleTimeoutSec", natICMPIdleTimeoutDefault)
	}
}

// setDefaultInt assigns v to m[key] only when the key is absent or holds the
// zero value, so a caller that sent an explicit timeout keeps it.
func setDefaultInt(m map[string]any, key string, v int) {
	switch cur := m[key].(type) {
	case nil:
		m[key] = v
	case float64:
		if cur == 0 {
			m[key] = v
		}
	}
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
