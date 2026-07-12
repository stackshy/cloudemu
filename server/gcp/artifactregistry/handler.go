// Package artifactregistry implements the artifactregistry.googleapis.com v1
// REST API as a server.Handler. Real google.golang.org/api/artifactregistry/v1
// clients pointed at this server CRUD repositories and list docker images
// end-to-end against the shared containerregistry driver.
//
// Coverage (v1 REST):
//
//	POST   /v1/projects/{p}/locations/{l}/repositories?repositoryId={id}        — Create repo (async)
//	GET    /v1/projects/{p}/locations/{l}/repositories/{id}                     — Get repo
//	GET    /v1/projects/{p}/locations/{l}/repositories                          — List repos
//	DELETE /v1/projects/{p}/locations/{l}/repositories/{id}                     — Delete repo (async)
//	GET    /v1/projects/{p}/locations/{l}/repositories/{id}/dockerImages        — List docker images
//
// The driver has no location dimension, so {l} is accepted and echoed but not
// used to partition state.
package artifactregistry

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	locationsSeg    = "locations"
	repositoriesSeg = "repositories"
	dockerImagesSeg = "dockerImages"
)

// minRepoCollectionParts is the segment count of a repositories collection
// path: [projects, {p}, locations, {l}, repositories].
const minRepoCollectionParts = 5

// Handler serves artifactregistry.googleapis.com v1 requests.
type Handler struct {
	registry crdriver.ContainerRegistry
}

// New returns an Artifact Registry handler backed by reg.
func New(reg crdriver.ContainerRegistry) *Handler {
	return &Handler{registry: reg}
}

type route struct {
	project    string
	location   string
	repository string // repo id; empty for the collection
	sub        string // "dockerImages" or ""
}

// parseRoute extracts the components of an Artifact Registry v1 path.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	// parts: [projects, {p}, locations, {l}, repositories, {id}?, {sub}?]
	if len(parts) < minRepoCollectionParts ||
		parts[0] != "projects" || parts[2] != locationsSeg || parts[4] != repositoriesSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	if len(parts) > minRepoCollectionParts {
		rt.repository = parts[5]
	}

	const subIdx = 6
	if len(parts) > subIdx {
		rt.sub = parts[subIdx]
	}

	return rt, true
}

// Matches claims artifactregistry v1 repository paths. Disjoint from the IAM
// handler (which matches serviceAccounts|roles at the same prefix).
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP dispatches on method and path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed Artifact Registry v1 path")
		return
	}

	if rt.repository == "" {
		h.serveCollection(w, r, &rt)
		return
	}

	h.serveResource(w, r, &rt)
}

// serveCollection handles the repositories collection (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.listRepositories(w, r, rt)
	case http.MethodPost:
		h.createRepository(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported repositories operation")
	}
}

// serveResource handles a single repository and its dockerImages sub-collection.
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.sub == dockerImagesSeg {
		if r.Method == http.MethodGet {
			h.listDockerImages(w, r, rt)
			return
		}

		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported dockerImages operation")

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRepository(w, r, rt)
	case http.MethodDelete:
		h.deleteRepository(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported repository operation")
	}
}
