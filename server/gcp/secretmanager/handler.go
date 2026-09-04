// Package secretmanager implements the secretmanager.googleapis.com v1 REST
// API as a server.Handler. Real google.golang.org/api/secretmanager/v1
// clients pointed at this server CRUD secrets, add versions, and access
// payloads end-to-end against the shared secrets driver.
//
// Coverage (v1 REST):
//
//	POST   /v1/projects/{p}/secrets?secretId={id}                 — Create secret
//	GET    /v1/projects/{p}/secrets/{id}                          — Get secret
//	PATCH  /v1/projects/{p}/secrets/{id}?updateMask=labels        — Patch secret
//	GET    /v1/projects/{p}/secrets                               — List secrets (paged)
//	DELETE /v1/projects/{p}/secrets/{id}                          — Delete secret
//	POST   /v1/projects/{p}/secrets/{id}:addVersion               — Add version
//	GET    /v1/projects/{p}/secrets/{id}:getIamPolicy             — Get IAM policy
//	POST   /v1/projects/{p}/secrets/{id}:setIamPolicy             — Set IAM policy
//	POST   /v1/projects/{p}/secrets/{id}:testIamPermissions       — Test IAM perms
//	GET    /v1/projects/{p}/secrets/{id}/versions                 — List versions (paged)
//	GET    /v1/projects/{p}/secrets/{id}/versions/{v}             — Get version
//	GET    /v1/projects/{p}/secrets/{id}/versions/{v}:access      — Access payload
//	POST   /v1/projects/{p}/secrets/{id}/versions/{v}:enable      — Enable version
//	POST   /v1/projects/{p}/secrets/{id}/versions/{v}:disable     — Disable version
//	POST   /v1/projects/{p}/secrets/{id}/versions/{v}:destroy     — Destroy version
//
// Versions are numbered by monotonic integer (1, 2, 3…) and addressable by that
// id; "latest" is accepted as a version alias, matching real Secret Manager. The
// driver seeds an initial (empty) version on create, so a freshly created
// secret carries one more version than the addVersion calls made against it.
//
// The version enable/disable/destroy verbs and secrets.patch each accept an
// optional "etag" optimistic-concurrency precondition: a non-empty etag that
// doesn't match the resource's currently stored one is rejected with 412
// conditionNotMet and the request has no effect; an omitted etag always
// succeeds.
package secretmanager

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	pathPrefix  = "/v1/projects/"
	secretsSeg  = "secrets"
	versionsSeg = "versions"

	verbAddVersion = "addVersion"
	verbAccess     = "access"
	verbDestroy    = "destroy"
	verbDisable    = "disable"
	verbEnable     = "enable"

	verbGetIam  = "getIamPolicy"
	verbSetIam  = "setIamPolicy"
	verbTestIam = "testIamPermissions"

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
	// gcp is the GCP-specific surface (version lifecycle, patch, IAM). It is
	// present whenever the backing driver implements driver.GCPSecrets (the GCP
	// mock always does); nil-checked so a non-GCP driver degrades to 501.
	gcp secretsdriver.GCPSecrets
}

// New returns a Secret Manager handler backed by s.
func New(s secretsdriver.Secrets) *Handler {
	h := &Handler{secrets: s}
	if g, ok := s.(secretsdriver.GCPSecrets); ok {
		h.gcp = g
	}

	return h
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
	if rt.verb != "" {
		h.serveSecretVerb(w, r, rt)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSecret(w, r, rt)
	case http.MethodPatch:
		h.patchSecret(w, r, rt)
	case http.MethodDelete:
		h.deleteSecret(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveSecretVerb dispatches the custom :verb methods on a secret resource.
func (h *Handler) serveSecretVerb(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.verb {
	case verbAddVersion:
		postOnly(w, r, func() { h.addVersion(w, r, rt) })
	case verbSetIam:
		postOnly(w, r, func() { h.setIamPolicy(w, r, rt) })
	case verbTestIam:
		postOnly(w, r, func() { h.testIamPermissions(w, r, rt) })
	case verbGetIam:
		getOnly(w, r, func() { h.getIamPolicy(w, r, rt) })
	default:
		writeUnsupported(w)
	}
}

// serveVersions dispatches /secrets/{id}/versions[...] requests, including
// the :access custom method.
func (h *Handler) serveVersions(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.listVer:
		getOnly(w, r, func() { h.listVersions(w, r, rt) })
	case rt.verb != "":
		h.serveVersionVerb(w, r, rt)
	default:
		getOnly(w, r, func() { h.getVersion(w, r, rt) })
	}
}

// serveVersionVerb dispatches the custom :verb methods on a version resource.
func (h *Handler) serveVersionVerb(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.verb {
	case verbAccess:
		getOnly(w, r, func() { h.accessVersion(w, r, rt) })
	case verbDestroy, verbDisable, verbEnable:
		postOnly(w, r, func() { h.mutateVersion(w, r, rt, rt.verb) })
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Secret Manager operation")
}

// getOnly runs fn for GET requests, else replies 400 unsupported.
func getOnly(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method == http.MethodGet {
		fn()
		return
	}

	writeUnsupported(w)
}

// postOnly runs fn for POST requests, else replies 400 unsupported.
func postOnly(w http.ResponseWriter, r *http.Request, fn func()) {
	if r.Method == http.MethodPost {
		fn()
		return
	}

	writeUnsupported(w)
}
