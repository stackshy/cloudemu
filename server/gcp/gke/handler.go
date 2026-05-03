// Package gke implements the GCP Kubernetes Engine (Container) API as a
// server.Handler. Real google.golang.org/api/container/v1 clients configured
// with a custom endpoint hit this handler the same way they hit
// container.googleapis.com.
//
// Wave-1 coverage (control plane only):
//
//	POST   /v1/projects/{p}/locations/{l}/clusters
//	GET    /v1/projects/{p}/locations/{l}/clusters
//	GET    /v1/projects/{p}/locations/{l}/clusters/{c}
//	PUT    /v1/projects/{p}/locations/{l}/clusters/{c}
//	DELETE /v1/projects/{p}/locations/{l}/clusters/{c}
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setLogging
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setMonitoring
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setMasterAuth
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setLegacyAbac
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setNetworkPolicy
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setMaintenancePolicy
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:setResourceLabels
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:startIpRotation
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:completeIpRotation
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools
//	GET    /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools
//	GET    /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}
//	PUT    /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}
//	DELETE /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}:setSize
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}:setAutoscaling
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}:setManagement
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/nodePools/{n}:rollback
//	GET    /v1/projects/{p}/locations/{l}/operations
//	GET    /v1/projects/{p}/locations/{l}/operations/{op}
//	POST   /v1/projects/{p}/locations/{l}/operations/{op}:cancel
//
// All mutating endpoints return Operation envelopes with status=DONE so SDK
// pollers terminate on the first response. Cluster.Endpoint and
// MasterAuth.ClusterCaCertificate carry stub values — see provider/gcp/gke
// for the Wave-2 deferral note.
//
// The /v1/projects/{p}/locations/{l}/ prefix is shared with Cloud Functions
// (functions/) and Cloud SQL (/v1/projects/{p}/instances). Matches narrows on
// the 4th segment so this handler claims ONLY clusters/operations URLs.
package gke

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/providers/gcp/gke"
)

const (
	pathPrefix      = "/v1/projects/"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20

	resourceClusters   = "clusters"
	resourceNodePools  = "nodePools"
	resourceOperations = "operations"
	locationsSeg       = "locations"

	// actionResX values tag the resource an action applies to.
	actionResCluster    = "cluster"
	actionResNodePool   = "nodePool"
	actionResOperation  = "operation"
	actionResCollection = "collection"
)

// Handler serves GKE container-API REST requests against a gke.Mock backend.
type Handler struct {
	gke *gke.Mock
}

// New returns a GKE handler backed by m.
func New(m *gke.Mock) *Handler {
	return &Handler{gke: m}
}

// Matches accepts /v1/projects/{p}/locations/{l}/{clusters|operations}/...
// paths. Anything else (functions, instances, databases) belongs to a
// different handler and falls through.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")

	const (
		idxScope    = 1 // "locations"
		idxResource = 3 // "clusters" | "operations"
	)

	if len(parts) <= idxResource {
		return false
	}

	if parts[idxScope] != locationsSeg {
		return false
	}

	res := parts[idxResource]
	if i := strings.Index(res, ":"); i >= 0 {
		res = res[:i]
	}

	return res == resourceClusters || res == resourceOperations
}

// gkePath holds the parsed components of a GKE URL.
type gkePath struct {
	project   string
	location  string
	resource  string // "clusters" or "operations"
	name      string // cluster name or operation name
	subRes    string // "nodePools" when present
	subName   string // node-pool name
	action    string // ":setLogging", ":setSize", ":cancel", etc. (no leading colon)
	actionRes string // resource the action applies to: "cluster" or "nodePool" or "operation"
}

// parsePath splits a GKE URL into a gkePath. Returns ok=false on malformed.
//

func parsePath(urlPath string) (gkePath, bool) {
	rest := strings.TrimPrefix(urlPath, pathPrefix)

	parts := strings.Split(rest, "/")

	const (
		minParts    = 4 // {project}/locations/{location}/{resource}
		idxProject  = 0
		idxScope    = 1
		idxLocation = 2
		idxResource = 3
		idxName     = 4
		idxSubRes   = 5
		idxSubName  = 6
	)

	if len(parts) < minParts || parts[idxScope] != locationsSeg {
		return gkePath{}, false
	}

	out := gkePath{
		project:  parts[idxProject],
		location: parts[idxLocation],
		resource: parts[idxResource],
	}

	// 4th segment may be "{resource}:{action}" for collection-level actions
	// (none today, but keep symmetry with cloudfunctions).
	if base, action, ok := splitColon(out.resource); ok {
		out.resource = base
		out.action = action
		out.actionRes = actionResCollection
	}

	if len(parts) > idxName {
		parseNameSegment(&out, parts[idxName])
	}

	if len(parts) > idxSubRes {
		out.subRes = parts[idxSubRes]
	}

	if len(parts) > idxSubName {
		parseSubNameSegment(&out, parts[idxSubName])
	}

	return out, true
}

func parseNameSegment(out *gkePath, seg string) {
	base, action, ok := splitColon(seg)
	if !ok {
		out.name = seg
		return
	}

	out.name = base
	out.action = action

	if out.resource == resourceOperations {
		out.actionRes = actionResOperation
	} else {
		out.actionRes = actionResCluster
	}
}

func parseSubNameSegment(out *gkePath, seg string) {
	base, action, ok := splitColon(seg)
	if !ok {
		out.subName = seg
		return
	}

	out.subName = base
	out.action = action
	out.actionRes = actionResNodePool
}

func splitColon(s string) (base, action string, ok bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", false
	}

	return s[:i], s[i+1:], true
}

// ServeHTTP routes based on the parsed path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "malformed path")
		return
	}

	switch p.resource {
	case resourceClusters:
		h.serveClusters(w, r, &p)
	case resourceOperations:
		h.serveOperations(w, r, &p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported resource: "+p.resource)
	}
}

func (h *Handler) serveClusters(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if p.action != "" && p.actionRes == actionResCluster {
		h.clusterAction(w, r, p)
		return
	}

	if p.subRes == resourceNodePools {
		h.serveNodePools(w, r, p)
		return
	}

	if p.name == "" {
		h.serveClusterCollection(w, r, p)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, p)
	case http.MethodPut:
		h.updateCluster(w, r, p)
	case http.MethodDelete:
		h.deleteCluster(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveClusterCollection(w http.ResponseWriter, r *http.Request, p *gkePath) {
	switch r.Method {
	case http.MethodPost:
		h.createCluster(w, r, p)
	case http.MethodGet:
		h.listClusters(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveNodePools(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if p.action != "" && p.actionRes == actionResNodePool {
		h.nodePoolAction(w, r, p)
		return
	}

	if p.subName == "" {
		switch r.Method {
		case http.MethodPost:
			h.createNodePool(w, r, p)
		case http.MethodGet:
			h.listNodePools(w, r, p)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getNodePool(w, r, p)
	case http.MethodPut:
		h.updateNodePool(w, r, p)
	case http.MethodDelete:
		h.deleteNodePool(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveOperations(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if p.action == "cancel" && p.actionRes == actionResOperation {
		h.cancelOperation(w, r, p)
		return
	}

	if p.name == "" {
		h.listOperations(w, r, p)
		return
	}

	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	op, err := h.gke.GetOperation(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) cancelOperation(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if err := h.gke.CancelOperation(r.Context(), p.location, p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	ops, err := h.gke.ListOperations(r.Context(), p.location)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listOperationsResp{Operations: make([]gkeOperation, 0, len(ops))}
	for i := range ops {
		out.Operations = append(out.Operations, toOperationResource(&ops[i], p.project))
	}

	writeJSON(w, http.StatusOK, out)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}
