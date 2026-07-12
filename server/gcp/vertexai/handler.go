// Package vertexai implements the Google Cloud Vertex AI
// (aiplatform.googleapis.com) REST API as a server.Handler. Real
// google.golang.org/api/aiplatform/v1 clients (and any REST caller) configured
// with a custom endpoint hit this handler the same way they hit
// {location}-aiplatform.googleapis.com.
//
// Control-plane mutations return google.longrunning.Operation envelopes with
// done=true AND the typed result populated in response (with its @type), so SDK
// pollers terminate on the first poll and unmarshal the resource straight off
// the operation. Job-family creates return the resource directly (synchronous).
// predict / generateContent are synchronous data-plane calls.
//
// The /v1/projects/{p}/locations/{l}/ prefix is shared with Cloud Functions,
// Cloud SQL and GKE; Matches narrows on the Vertex collection segment so this
// handler claims only its own URLs. It also claims /v1/publishers/ for the
// Model Garden generateContent surface.
//
// LRO polling note: the operations collection
// (.../locations/{l}/operations/{id}) is intentionally NOT claimed here — the
// GKE handler, registered ahead of Vertex on the shared prefix, already owns
// it. This is safe because every Vertex mutation completes done-on-arrival with
// its result inlined (above), so a client never needs to poll. If a future
// non-done Vertex op is introduced, route operations through a shared
// per-location operations handler before relying on polling.
package vertexai

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	publishersPrefix = "/v1/publishers/"
	locationsSeg     = "locations"
	maxBodyBytes     = 6 << 20

	actionCancel                = "cancel"
	actionGenerateContent       = "generateContent"
	actionStreamGenerateContent = "streamGenerateContent"
)

// vertexCollections are the resource collections this handler serves. Listed
// explicitly so it never shadows the sibling functions/instances/clusters
// handlers on the shared locations prefix.
//
//nolint:gochecknoglobals // immutable routing set shared by Matches and dispatch
var vertexCollections = map[string]bool{
	"datasets": true, "models": true, "endpoints": true,
	"customJobs": true, "batchPredictionJobs": true,
	"hyperparameterTuningJobs": true, "trainingPipelines": true,
	"pipelineJobs": true, "tuningJobs": true, "cachedContents": true,
	"featurestores": true, "featureGroups": true, "featureOnlineStores": true,
	"indexes": true, "indexEndpoints": true, "metadataStores": true,
	"tensorboards": true, "schedules": true, "notebookRuntimes": true,
	"notebookRuntimeTemplates": true,
}

// collectionHandler serves one Vertex collection's requests.
type collectionHandler func(http.ResponseWriter, *http.Request, *vPath)

// Handler serves Vertex AI REST requests against a vertexai driver.
type Handler struct {
	svc    driver.VertexAI
	routes map[string]collectionHandler
}

// New returns a Vertex AI handler backed by svc.
func New(svc driver.VertexAI) *Handler {
	h := &Handler{svc: svc}
	h.routes = map[string]collectionHandler{
		"models":                   h.serveModels,
		"endpoints":                h.serveEndpoints,
		"datasets":                 h.serveDatasets,
		"customJobs":               h.serveCustomJobs,
		"batchPredictionJobs":      h.serveBatchPredictionJobs,
		"hyperparameterTuningJobs": h.serveHyperparameterTuningJobs,
		"trainingPipelines":        h.serveTrainingPipelines,
		"pipelineJobs":             h.servePipelineJobs,
		"tuningJobs":               h.serveTuningJobs,
		"cachedContents":           h.serveCachedContents,
		"featurestores":            h.serveFeaturestores,
		"featureGroups":            h.serveFeatureGroups,
		"featureOnlineStores":      h.serveFeatureOnlineStores,
		"indexes":                  h.serveIndexes,
		"indexEndpoints":           h.serveIndexEndpoints,
		"metadataStores":           h.serveMetadataStores,
		"tensorboards":             h.serveTensorboards,
		"schedules":                h.serveSchedules,
		"notebookRuntimes":         h.serveNotebookRuntimes,
		"notebookRuntimeTemplates": h.serveNotebookRuntimeTemplates,
	}

	return h
}

// Matches claims the Vertex collection URLs and the publishers generateContent
// surface.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, publishersPrefix) {
		return true
	}

	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")

	const idxScope, idxResource = 1, 3

	if len(parts) <= idxResource || parts[idxScope] != locationsSeg {
		return false
	}

	return vertexCollections[stripAction(parts[idxResource])]
}

// vPath is a parsed Vertex REST path.
type vPath struct {
	project    string
	location   string
	collection string
	name       string // full resource name when addressing a specific resource
	subRes     string // e.g. "features" / "featureViews" / "operations"
	subName    string
	action     string // ":predict", ":deployModel", ":upload", ... (no leading colon)
}

// parsePath splits a Vertex projects/locations URL.
func parsePath(urlPath string) (vPath, bool) {
	parts := strings.Split(strings.TrimPrefix(urlPath, pathPrefix), "/")

	const (
		minParts    = 4
		idxProject  = 0
		idxLocation = 2
		idxResource = 3
		idxName     = 4
		idxSubRes   = 5
		idxSubName  = 6
	)

	if len(parts) < minParts {
		return vPath{}, false
	}

	p := vPath{project: parts[idxProject], location: parts[idxLocation]}
	p.collection, p.action = splitActionPair(parts[idxResource])

	if len(parts) > idxName {
		seg, act := splitActionPair(parts[idxName])
		p.name = "projects/" + p.project + "/locations/" + p.location + "/" + p.collection + "/" + seg

		if act != "" {
			p.action = act
		}
	}

	if len(parts) > idxSubRes {
		p.subRes, _ = splitActionPair(parts[idxSubRes])
	}

	if len(parts) > idxSubName {
		seg, act := splitActionPair(parts[idxSubName])
		p.subName = seg

		if act != "" {
			p.action = act
		}
	}

	return p, true
}

// stripAction returns a path segment without any ":action" suffix.
func stripAction(seg string) string {
	if i := strings.Index(seg, ":"); i >= 0 {
		return seg[:i]
	}

	return seg
}

// splitActionPair splits "seg:action" into ("seg", "action").
func splitActionPair(seg string) (name, action string) {
	if i := strings.Index(seg, ":"); i >= 0 {
		return seg[:i], seg[i+1:]
	}

	return seg, ""
}

// ServeHTTP dispatches Vertex requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, publishersPrefix) {
		h.servePublishers(w, r)

		return
	}

	p, ok := parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "unsupported path: "+r.URL.Path)

		return
	}

	serve, ok := h.routes[p.collection]
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "unsupported collection: "+p.collection)

		return
	}

	serve(w, r, &p)
}
