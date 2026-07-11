// Package secretmanager implements the secretmanager.googleapis.com v1 REST
// API as a server.Handler. Real google.golang.org/api/secretmanager/v1
// clients pointed at this server CRUD secrets, add versions, and access
// payloads end-to-end against the shared secrets driver.
//
// Coverage (v1 REST):
//
//	POST   /v1/projects/{p}/secrets?secretId={id}              — Create secret
//	GET    /v1/projects/{p}/secrets/{id}                       — Get secret
//	GET    /v1/projects/{p}/secrets                            — List secrets
//	DELETE /v1/projects/{p}/secrets/{id}                       — Delete secret
//	POST   /v1/projects/{p}/secrets/{id}:addVersion            — Add version
//	GET    /v1/projects/{p}/secrets/{id}/versions              — List versions
//	GET    /v1/projects/{p}/secrets/{id}/versions/{v}          — Get version
//	GET    /v1/projects/{p}/secrets/{id}/versions/{v}:access   — Access payload
//
// "latest" is accepted as a version alias, matching real Secret Manager. The
// driver seeds an initial (empty) version on create, so a freshly created
// secret carries one more version than the addVersion calls made against it.
package secretmanager

import (
	"net/http"
	"strings"

	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

const (
	pathPrefix  = "/v1/projects/"
	secretsSeg  = "secrets"
	versionsSeg = "versions"

	verbAddVersion = "addVersion"
	verbAccess     = "access"

	latestAlias = "latest"
)

// Path-tail segment counts after the [projects, {p}, secrets] head: a bare
// secret, its versions collection, and a specific version.
const (
	minSecretCollectionParts = 3 // [projects, {p}, secrets]
	restVersionsCollection   = 2 // [{id}, versions]
	restVersionResource      = 3 // [{id}, versions, {v}]
)

// Handler serves secretmanager.googleapis.com v1 requests.
type Handler struct {
	secrets secretsdriver.Secrets
}

// New returns a Secret Manager handler backed by s.
func New(s secretsdriver.Secrets) *Handler {
	return &Handler{secrets: s}
}

type route struct {
	project string
	secret  string // secret id; empty for the collection
	version string // version id; empty unless a versions/{v} path
	listVer bool   // true for the versions collection
	verb    string // "addVersion" or "access" colon-verb, if any
}

// parseRoute extracts the components of a Secret Manager v1 path. The
// trailing segment may carry a ":verb" suffix (addVersion, access).
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minSecretCollectionParts || parts[0] != "projects" || parts[2] != secretsSeg {
		return route{}, false
	}

	rt := route{project: parts[1]}
	rest := parts[minSecretCollectionParts:]

	if len(rest) == 0 {
		return rt, true
	}

	rt.secret, rt.verb, _ = strings.Cut(rest[0], ":")
	if len(rest) == 1 {
		return rt, true
	}

	if rest[1] != versionsSeg || len(rest) > restVersionResource {
		return route{}, false
	}

	if len(rest) == restVersionsCollection {
		rt.listVer = true
		return rt, true
	}

	rt.version, rt.verb, _ = strings.Cut(rest[2], ":")

	return rt, true
}

// Matches claims /v1/projects/{p}/secrets[...] paths — disjoint from IAM
// (serviceAccounts|roles), Artifact Registry (locations/...), and Cloud Asset
// among the /v1/projects/ family. Registered before Firestore's permissive
// fallback.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Secret Manager path")
		return
	}

	switch {
	case rt.secret == "":
		h.serveCollection(w, r, rt)
	case rt.listVer || rt.version != "":
		h.serveVersions(w, r, rt)
	default:
		h.serveSecret(w, r, rt)
	}
}

// serveCollection dispatches /secrets collection requests.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createSecret(w, r, rt)
	case http.MethodGet:
		h.listSecrets(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveSecret dispatches /secrets/{id} resource requests, including the
// :addVersion custom method.
func (h *Handler) serveSecret(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.verb == verbAddVersion && r.Method == http.MethodPost:
		h.addVersion(w, r, rt)
	case rt.verb == "" && r.Method == http.MethodGet:
		h.getSecret(w, r, rt)
	case rt.verb == "" && r.Method == http.MethodDelete:
		h.deleteSecret(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveVersions dispatches /secrets/{id}/versions[...] requests, including
// the :access custom method.
func (h *Handler) serveVersions(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.listVer && r.Method == http.MethodGet:
		h.listVersions(w, r, rt)
	case rt.verb == verbAccess && r.Method == http.MethodGet:
		h.accessVersion(w, r, rt)
	case rt.verb == "" && r.Method == http.MethodGet:
		h.getVersion(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Secret Manager operation")
}
