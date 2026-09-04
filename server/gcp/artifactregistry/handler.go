// Package artifactregistry implements the artifactregistry.googleapis.com v1
// REST API as a server.Handler. Real google.golang.org/api/artifactregistry/v1
// clients pointed at this server CRUD repositories, patch them, manage repo
// IAM, and list docker images / packages / versions / tags / files end-to-end
// against the shared containerregistry driver.
//
// Coverage (v1 REST):
//
//	POST   .../repositories?repositoryId={id}              — Create repo (async)
//	GET    .../repositories/{id}                           — Get repo
//	GET    .../repositories                                — List repos (paged)
//	PATCH  .../repositories/{id}                           — Update labels/description/mode
//	DELETE .../repositories/{id}                           — Delete repo (async)
//	GET/POST .../repositories/{id}:{get,set}IamPolicy      — Repo IAM
//	POST   .../repositories/{id}:testIamPermissions        — Repo IAM
//	GET    .../repositories/{id}/dockerImages             — List docker images (paged)
//	GET    .../repositories/{id}/packages                 — List packages (paged)
//	GET    .../repositories/{id}/packages/{pkg}           — Get package
//	GET    .../repositories/{id}/packages/{pkg}/versions  — List versions (paged)
//	DELETE .../repositories/{id}/packages/{pkg}/versions/{v} — Delete version
//	GET    .../repositories/{id}/packages/{pkg}/tags      — List tags (paged)
//	POST   .../packages/{pkg}/tags?tagId={id}             — Create tag
//	PATCH  .../packages/{pkg}/tags/{id}                   — Patch tag (blocked by immutableTags)
//	DELETE .../packages/{pkg}/tags/{id}                   — Delete tag (blocked by immutableTags)
//	GET    .../repositories/{id}/files                    — List files (paged)
//
// The driver has no location dimension, so {l} is accepted and echoed but not
// used to partition state.
package artifactregistry

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	locationsSeg    = "locations"
	repositoriesSeg = "repositories"
	operationsSeg   = "operations"
	dockerImagesSeg = "dockerImages"
)

// minRepoCollectionParts is the segment count of a repositories collection
// path: [projects, {p}, locations, {l}, repositories].
const minRepoCollectionParts = 5

// Handler serves artifactregistry.googleapis.com v1 requests.
type Handler struct {
	registry crdriver.ContainerRegistry

	// policies stores repo IAM policies set via :setIamPolicy, keyed by the
	// repository resource name. CloudEmu does not enforce IAM; it stores the
	// policy so setIamPolicy → getIamPolicy round-trips (Terraform's
	// google_artifact_registry_repository_iam_* flow).
	mu       sync.RWMutex
	policies map[string]*iamPolicy

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns an Artifact Registry handler backed by reg.
func New(reg crdriver.ContainerRegistry) *Handler {
	return &Handler{registry: reg, policies: make(map[string]*iamPolicy)}
}

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

type route struct {
	project    string
	location   string
	repository string // repo id; empty for the collection
	verb       string // colon action on the repository (getIamPolicy, ...)
	sub        string // "dockerImages" | "packages" | "files" | ""
	pkg        string // package id (when sub == packages)
	pkgSub     string // "versions" | "tags" | ""
	pkgSubID   string // version or tag id (when pkgSub set)
	fileID     string // file id (when sub == files)
	operation  string // operation id when this is an /operations/{op} path
}

// parseRoute extracts the components of an Artifact Registry v1 path.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	// parts: [projects, {p}, locations, {l}, {repositories|operations}, ...]
	if len(parts) < minRepoCollectionParts ||
		parts[0] != "projects" || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	// LRO polling: GAPIC clients (.Wait()) GET the operation returned by a
	// create/delete. The shared lro handler (registered first) owns these, but
	// keep the route so a standalone AR handler still answers polls.
	if parts[4] == operationsSeg {
		if len(parts) > minRepoCollectionParts {
			rt.operation = parts[5]
		}

		return rt, true
	}

	if parts[4] != repositoriesSeg {
		return route{}, false
	}

	parseRepoTail(parts, &rt)

	return rt, true
}

// parseRepoTail fills the repository, colon-verb, and sub-collection fields from
// the path segments after "repositories".
func parseRepoTail(parts []string, rt *route) {
	const (
		repoIdx = 5
		subIdx  = 6
		idIdx   = 7
		pkgIdx  = 8
	)

	if len(parts) <= repoIdx {
		return
	}

	// A colon verb (repositories/{id}:getIamPolicy) attaches to the repo id.
	rt.repository, rt.verb = splitVerb(parts[repoIdx])

	if len(parts) <= subIdx {
		return
	}

	rt.sub = parts[subIdx]

	switch rt.sub {
	case packagesSeg:
		if len(parts) > idIdx {
			rt.pkg = parts[idIdx]
		}

		if len(parts) > pkgIdx {
			rt.pkgSub = parts[pkgIdx]
		}

		const pkgIDIdx = 9
		if len(parts) > pkgIDIdx {
			rt.pkgSubID = parts[pkgIDIdx]
		}
	case filesSeg:
		if len(parts) > idIdx {
			rt.fileID = parts[idIdx]
		}
	}
}

// splitVerb separates a "resource:verb" segment into its parts.
func splitVerb(seg string) (resource, verb string) {
	if idx := strings.LastIndex(seg, ":"); idx >= 0 {
		return seg[:idx], seg[idx+1:]
	}

	return seg, ""
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

	if rt.operation != "" {
		gcprest.WriteJSON(w, http.StatusOK, operationJSON{
			Name: "projects/" + rt.project + "/locations/" + rt.location + "/operations/" + rt.operation,
			Done: true,
		})

		return
	}

	if rt.verb != "" {
		h.serveIAM(w, r, &rt)
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

// serveResource handles a single repository and its sub-collections.
func (h *Handler) serveResource(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.sub != "" {
		h.serveSub(w, r, rt)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRepository(w, r, rt)
	case http.MethodPatch:
		h.patchRepository(w, r, rt)
	case http.MethodDelete:
		h.deleteRepository(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported repository operation")
	}
}

// serveSub dispatches the repository sub-collections (docker images, packages,
// versions, tags, files). Reads are GETs; packages and versions also accept
// DELETE (packages.delete / versions.delete), routed through servePackages.
func (h *Handler) serveSub(w http.ResponseWriter, r *http.Request, rt *route) {
	// The packages sub-tree owns both GET (list/get) and DELETE (package/version
	// removal); it dispatches on method itself.
	if rt.sub == packagesSeg {
		h.servePackages(w, r, rt)
		return
	}

	if r.Method != http.MethodGet {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported "+rt.sub+" operation")
		return
	}

	switch rt.sub {
	case dockerImagesSeg:
		h.listDockerImages(w, r, rt)
	case filesSeg:
		h.listFiles(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported sub-collection "+rt.sub)
	}
}
