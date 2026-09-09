// Package dataform implements the Google Cloud Dataform control plane
// (dataform.googleapis.com) as a server.Handler on the /v1beta1/ version prefix.
// Dataform ships a v1beta1 API only — there is no /v1/ — so both the
// hashicorp/google-beta provider's google_dataform_repository resource (the
// resource lives only in google-beta) and a real
// google.golang.org/api/dataform/v1beta1 client target /v1beta1/ unchanged.
//
// Coverage (region-scoped repository control plane only, synchronous REST — no
// LRO):
//
//	POST   /v1beta1/…/repositories?repositoryId=        — CreateRepository
//	GET    /v1beta1/…/repositories                      — ListRepositories
//	GET    /v1beta1/…/repositories/{repo}               — GetRepository
//	PATCH  /v1beta1/…/repositories/{repo}?updateMask=   — PatchRepository
//	DELETE /v1beta1/…/repositories/{repo}?force=        — DeleteRepository
//
// Every RPC returns the resource (or an empty object for delete) directly with
// no google.longrunning.Operation wrapper. The repositories resource-segment
// guard keeps this handler disjoint from every other /v1beta1/projects/ handler.
//
// The nested releaseConfigs/workflowConfigs collections and the
// workspace/compilationResult/workflowInvocation data plane are out of scope
// (see BUILDOUT_BACKLOG.md).
package dataform

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dfdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"
)

const (
	// apiV1Beta1 is the only API version Dataform exposes.
	apiV1Beta1 = "v1beta1"

	projectsSeg     = "projects"
	locationsSeg    = "locations"
	repositoriesSeg = "repositories"

	// minParts is [projects, {p}, locations, {loc}, repositories].
	minParts = 5
)

// Handler serves dataform.googleapis.com v1beta1 requests against a Dataform
// driver.
type Handler struct {
	db dfdriver.Dataform
}

// route holds the parsed components of a Dataform repositories path. repo is the
// repository id, empty for a collection request.
type route struct {
	project  string
	location string
	repo     string
}

// New returns a Dataform handler backed by db.
func New(db dfdriver.Dataform) *Handler { return &Handler{db: db} }

// parseRoute extracts the components of a Dataform repositories path under the
// /v1beta1/ prefix. It accepts the repositories collection and item forms.
func parseRoute(urlPath string) (route, bool) {
	prefix := "/" + apiV1Beta1 + "/"
	if !strings.HasPrefix(urlPath, prefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, prefix), "/")
	if len(parts) < minParts || parts[0] != projectsSeg || parts[2] != locationsSeg || parts[4] != repositoriesSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	switch len(parts) {
	case minParts: // repositories collection
		return rt, true
	case minParts + 1: // repositories/{repo} item
		rt.repo = parts[5]
		return rt, rt.repo != ""
	default:
		return route{}, false
	}
}

// Matches claims the Dataform repositories hierarchy. The repositories
// resource-segment guard keeps it disjoint from every other /v1beta1/projects/
// handler.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)

	return ok
}

// ServeHTTP routes on whether the path addresses a collection or an item, then
// by method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Dataform path")
		return
	}

	if rt.repo == "" {
		h.serveCollection(w, r, &rt)
		return
	}

	h.serveItem(w, r, &rt)
}

// serveCollection routes a repositories collection request (POST create, GET
// list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createRepository(w, r, rt)
	case http.MethodGet:
		h.listRepositories(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem routes a repository item request (GET get, PATCH patch, DELETE
// delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getRepository(w, r, rt)
	case http.MethodPatch:
		h.patchRepository(w, r, rt)
	case http.MethodDelete:
		h.deleteRepository(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
