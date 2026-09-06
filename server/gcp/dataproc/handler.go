// Package dataproc implements the Google Cloud Dataproc cluster control plane
// (dataproc.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/dataproc/v1 clients, gcloud, and the Terraform google
// provider's google_dataproc_cluster resource hit this handler unchanged.
//
// Coverage (cluster control plane only):
//
//	POST   /v1/projects/{p}/regions/{r}/clusters              — CreateCluster (LRO)
//	GET    /v1/projects/{p}/regions/{r}/clusters              — ListClusters
//	GET    /v1/projects/{p}/regions/{r}/clusters/{c}          — GetCluster
//	PATCH  /v1/projects/{p}/regions/{r}/clusters/{c}          — UpdateCluster (LRO, updateMask query param)
//	DELETE /v1/projects/{p}/regions/{r}/clusters/{c}          — DeleteCluster (LRO)
//	GET    /v1/projects/{p}/regions/{r}/operations/{op}       — poll (always done)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting cluster embedded in `response`, and a created cluster is RUNNING
// immediately, so an SDK or Terraform LRO wait terminates on the first poll
// instead of hanging.
//
// Region-scoped operations: Dataproc's operations live under
// /v1/projects/{p}/regions/{r}/operations, NOT the /locations/ space the shared
// GCP LRO poller owns, so this handler serves its own operation polls. The
// /v1/projects/{p}/regions/{r}/ prefix looks superficially like regional Compute
// (/regions/{r}/), but Compute is served under the /compute/v1/ prefix, so the
// two never collide; Matches additionally narrows on the 4th path segment
// (clusters|operations) so nothing else in the /v1/projects/ space is claimed.
package dataproc

import (
	"encoding/json"
	"net/http"
	"strings"

	dp "google.golang.org/api/dataproc/v1"
	"google.golang.org/api/googleapi"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

const (
	pathPrefix = "/v1/projects/"

	segRegions    = "regions"
	segClusters   = "clusters"
	segOperations = "operations"

	// Path-segment counts below the /v1/projects/ prefix.
	partsCollection = 4 // {p}/regions/{r}/{resource}
	partsItem       = 5 // {p}/regions/{r}/{resource}/{name}

	idxRegionsKw = 1 // parts[1] == "regions"
	idxRegion    = 2 // parts[2] == region
	idxResource  = 3 // parts[3] == "clusters" | "operations"
	idxName      = 4 // parts[4] == cluster or operation name
)

// clusterTypeURL is the google.protobuf.Any type URL for a Dataproc Cluster,
// embedded in a completed operation's `response` so an SDK that unpacks the Any
// resolves the cluster.
const clusterTypeURL = "type.googleapis.com/google.cloud.dataproc.v1.Cluster"

// Handler serves Dataproc cluster-control-plane requests against a dataproc
// driver.
type Handler struct {
	db dpdriver.Dataproc
}

// New returns a Dataproc handler backed by db.
func New(db dpdriver.Dataproc) *Handler { return &Handler{db: db} }

// trimParts splits the path below the /v1/projects/ prefix into its segments.
func trimParts(urlPath string) []string {
	return strings.Split(strings.TrimPrefix(urlPath, pathPrefix), "/")
}

// Matches claims only /v1/projects/{p}/regions/{r}/{clusters|operations}[/…]
// paths. The 4th-segment narrowing keeps every other /v1/projects/ handler
// (Firestore, Cloud SQL, Spanner, …) and any other region-scoped resource
// untouched, and the /compute/v1/ Compute handler is on a different prefix
// entirely.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := trimParts(r.URL.Path)
	if len(parts) < partsCollection || parts[idxRegionsKw] != segRegions {
		return false
	}

	switch parts[idxResource] {
	case segClusters, segOperations:
		return true
	default:
		return false
	}
}

// ServeHTTP routes a matched Dataproc request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := trimParts(r.URL.Path)
	if len(parts) < partsCollection || parts[idxRegionsKw] != segRegions {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Dataproc path")
		return
	}

	project, region := parts[0], parts[idxRegion]

	switch parts[idxResource] {
	case segClusters:
		h.serveClusters(w, r, project, region, parts)
	case segOperations:
		h.serveOperation(w, r)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Dataproc path")
	}
}

func (h *Handler) serveClusters(w http.ResponseWriter, r *http.Request, project, region string, parts []string) {
	if len(parts) == partsCollection {
		switch r.Method {
		case http.MethodPost:
			h.createCluster(w, r, project, region)
		case http.MethodGet:
			h.listClusters(w, r, project, region)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	name := parts[idxName]

	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, project, region, name)
	case http.MethodPatch:
		h.patchCluster(w, r, project, region, name)
	case http.MethodDelete:
		h.deleteCluster(w, r, project, region, name)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveOperation resolves a (done) long-running operation poll. The operation
// resource name is the request path without the /v1/ version prefix. The
// completed operation re-embeds the affected cluster in `response` (fetched live
// from the store), because clients re-poll the operation by name after a mutation
// and read that response to populate the resource.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, h.operationResponse(r, op)))
}

// operationResponse re-fetches the cluster an operation acted on so a poll can
// replay it. A delete operation (or a cluster since removed) yields nil.
func (h *Handler) operationResponse(r *http.Request, op *dpdriver.Operation) json.RawMessage {
	if op.Type == "delete" || op.TargetName == "" {
		return nil
	}

	project, region, name, ok := parseClusterName(op.TargetName)
	if !ok {
		return nil
	}

	c, err := h.db.GetCluster(r.Context(), project, region, name)
	if err != nil {
		return nil
	}

	return clusterResponseAny(toWireCluster(c))
}

// parseClusterName splits projects/{p}/regions/{r}/clusters/{c} into its parts.
func parseClusterName(full string) (project, region, name string, ok bool) {
	const (
		wantLen  = 6
		iProject = 1
		iRegion  = 3
		iName    = 5
	)

	p := strings.Split(full, "/")
	if len(p) != wantLen || p[0] != "projects" || p[2] != segRegions || p[4] != segClusters {
		return "", "", "", false
	}

	return p[iProject], p[iRegion], p[iName], true
}

// doneOperation builds a completed google.longrunning.Operation carrying the
// cluster response Any, so an SDK or Terraform caller observes a terminal
// operation at once.
func doneOperation(name string, response json.RawMessage) *dp.Operation {
	return &dp.Operation{Name: name, Done: true, Response: googleapi.RawMessage(response)}
}

// clusterResponseAny wraps a wire cluster as a google.protobuf.Any (a flat JSON
// object with an "@type" discriminator), the shape a completed operation's
// `response` carries.
func clusterResponseAny(c *dp.Cluster) json.RawMessage {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + clusterTypeURL + `"`)

	out, err := json.Marshal(fields)
	if err != nil {
		return nil
	}

	return out
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
