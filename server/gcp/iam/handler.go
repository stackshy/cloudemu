// Package iam implements the GCP iam.googleapis.com v1 REST API as a
// server.Handler. Real google.golang.org/api/iam/v1 clients pointed at this
// server CRUD ServiceAccounts, custom Roles, and ServiceAccountKeys
// end-to-end.
//
// MVP coverage (v1 REST):
//
//	POST   /v1/projects/{p}/serviceAccounts                                       — Create SA
//	GET    /v1/projects/{p}/serviceAccounts/{email}                               — Get SA
//	GET    /v1/projects/{p}/serviceAccounts                                       — List SAs
//	DELETE /v1/projects/{p}/serviceAccounts/{email}                               — Delete SA
//	PATCH  /v1/projects/{p}/serviceAccounts/{email}                               — Update SA
//	POST   /v1/projects/{p}/serviceAccounts/{email}/keys                          — Create key
//	GET    /v1/projects/{p}/serviceAccounts/{email}/keys/{keyId}                  — Get key
//	GET    /v1/projects/{p}/serviceAccounts/{email}/keys                          — List keys
//	DELETE /v1/projects/{p}/serviceAccounts/{email}/keys/{keyId}                  — Delete key
//	POST   /v1/projects/{p}/serviceAccounts/{email}/keys/{keyId}:disable          — Disable key
//	POST   /v1/projects/{p}/serviceAccounts/{email}/keys/{keyId}:enable           — Enable key
//	POST   /v1/projects/{p}/roles                                                 — Create role
//	GET    /v1/projects/{p}/roles/{roleId}                                        — Get role
//	GET    /v1/projects/{p}/roles                                                 — List roles
//	DELETE /v1/projects/{p}/roles/{roleId}                                        — Delete role
//	PATCH  /v1/projects/{p}/roles/{roleId}                                        — Update role
//
// All state lives in the shared iamdriver.IAM:
//
//   - Driver Users back ServiceAccounts (driver User.Name == SA email).
//   - Driver Roles back custom Roles (the SA-style Permissions list is
//     stashed in AssumeRolePolicyDoc as JSON).
//   - Driver AccessKeys back SA Keys (AccessKey.UserName == SA email,
//     AccessKeyID == key id).
//
// Resource-level IAM policy bindings (getIamPolicy / setIamPolicy on
// projects, buckets, etc.) are out of scope — those live on individual
// resources, not on iam.googleapis.com itself.
package iam

import (
	"net/http"
	"strings"
	"sync"

	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	pathPrefix         = "/v1/projects/"
	serviceAccountsSeg = "serviceAccounts"
	rolesSeg           = "roles"
	keysSeg            = "keys"

	undeleteVerb = "undelete"
)

// Handler serves iam.googleapis.com v1 REST requests against the IAM driver.
//
// Service-account resource policies, the enabled/disabled bit, per-key
// disabled bit, policy etag versions and soft-deleted resources have no
// place in the portable IAM driver, so they're tracked here keyed by SA
// email / role id / key id. The driver hard-deletes, so undelete is served
// from these local tombstones.
type Handler struct {
	iam iamdriver.IAM

	mu            sync.RWMutex
	saPolicy      map[string]*iamPolicy   // SA email -> resource policy
	policyVersion map[string]int          // SA email -> monotonic policy etag version
	disabled      map[string]bool         // SA email -> disabled
	keyDisabled   map[string]bool         // key id -> disabled
	deletedSA     map[string]*deletedSA   // SA email -> soft-deleted account
	deletedRole   map[string]*deletedRole // role id -> soft-deleted role
}

// deletedSA is a tombstone captured at delete time so undelete can restore the
// account with the same project, metadata and disabled bit.
type deletedSA struct {
	project     string
	displayName string
	description string
	disabled    bool
}

// deletedRole is a tombstone for a soft-deleted custom role.
type deletedRole struct {
	project string
	props   roleProps
}

// New returns an IAM handler backed by drv.
func New(drv iamdriver.IAM) *Handler {
	return &Handler{
		iam:           drv,
		saPolicy:      make(map[string]*iamPolicy),
		policyVersion: make(map[string]int),
		disabled:      make(map[string]bool),
		keyDisabled:   make(map[string]bool),
		deletedSA:     make(map[string]*deletedSA),
		deletedRole:   make(map[string]*deletedRole),
	}
}

// Matches returns true for any /v1/projects/{p}/{serviceAccounts|roles}[/…]
// path. The catch-all match is safe because the GCP server registers more
// specific handlers (compute, networks, gcs, …) ahead of this one — there's
// no other IAM handler in the GCP namespace that this would shadow.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	tail := strings.TrimPrefix(r.URL.Path, pathPrefix)

	parts := strings.Split(tail, "/")
	// parts: [project, kind, …]
	if len(parts) < 2 { //nolint:mnd // need at least project + kind
		return false
	}

	return parts[1] == serviceAccountsSeg || parts[1] == rolesSeg
}

// route is the parsed shape of an IAM URL after the /v1/projects/{p}/ prefix.
type route struct {
	project string
	kind    string // serviceAccountsSeg or rolesSeg
	name    string // SA email or role id, or "" for a collection
	subKind string // keysSeg, or "" for non-key paths
	subName string // key id, or "" for the collection
	verb    string // trailing ":method" (getIamPolicy, signBlob, …), or ""
}

// parseRoute splits the URL after /v1/projects/. Returns ok=false if the
// shape doesn't match what the IAM v1 SDK emits.
func parseRoute(urlPath string) (route, bool) {
	tail := strings.TrimPrefix(urlPath, pathPrefix)
	tail = strings.TrimRight(tail, "/")

	// GCP one-off methods are POSTs to "…/{resource}:method". Split the trailing
	// ":method" off the final segment before path splitting (SA emails and role
	// ids contain no ':').
	var verb string
	if i := strings.LastIndex(tail, ":"); i >= 0 {
		verb = tail[i+1:]
		tail = tail[:i]
	}

	parts := strings.Split(tail, "/")
	if len(parts) < 2 { //nolint:mnd // need at least project + kind
		return route{}, false
	}

	r := route{project: parts[0], kind: parts[1], verb: verb}

	if len(parts) >= 3 { //nolint:mnd // optional resource name segment
		r.name = parts[2]
	}

	if len(parts) >= 4 { //nolint:mnd // optional sub-resource (keys)
		r.subKind = parts[3]
	}

	if len(parts) >= 5 { //nolint:mnd // optional sub-resource name (key id)
		r.subName = parts[4]
	}

	return r, true
}

// ServeHTTP routes by URL shape and HTTP verb.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalidArgument", "malformed IAM v1 path")
		return
	}

	switch rt.kind {
	case serviceAccountsSeg:
		h.routeServiceAccounts(w, r, &rt)
	case rolesSeg:
		h.routeRoles(w, r, &rt)
	default:
		writeError(w, http.StatusNotFound, "notFound",
			"unknown IAM resource: "+rt.kind)
	}
}

// routeServiceAccounts dispatches the /serviceAccounts/* surface.
func (h *Handler) routeServiceAccounts(w http.ResponseWriter, r *http.Request, rt *route) {
	// One-off ":method" calls (getIamPolicy, signBlob, …) are POSTs on a
	// specific service account. A verb on a keys sub-resource (…/keys/{id}:disable)
	// parses into the same rt.verb + rt.name shape but must be routed to the
	// key-level dispatch below instead, so this only fires when there's no
	// sub-resource in play.
	if rt.verb != "" && rt.name != "" && rt.subKind == "" {
		h.routeSAVerb(w, r, rt)
		return
	}

	switch {
	// Collection: POST create, GET list.
	case rt.name == "":
		h.routeServiceAccountsCollection(w, r, rt.project)
	// Single SA: GET / DELETE / PATCH.
	case rt.subKind == "":
		h.routeSingleServiceAccount(w, r, rt)
	// SA keys sub-resource.
	case rt.subKind == keysSeg:
		h.routeServiceAccountKeys(w, r, rt)
	default:
		writeError(w, http.StatusNotFound, "notFound",
			"unknown serviceAccount sub-resource: "+rt.subKind)
	}
}

// routeServiceAccountsCollection dispatches the /serviceAccounts collection
// endpoint: POST create, GET list.
func (h *Handler) routeServiceAccountsCollection(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodPost:
		h.createServiceAccount(w, r, project)
	case http.MethodGet:
		h.listServiceAccounts(w, r, project)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"unsupported verb on serviceAccounts collection: "+r.Method)
	}
}

// routeSingleServiceAccount dispatches GET / DELETE / PATCH on one SA.
func (h *Handler) routeSingleServiceAccount(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getServiceAccount(w, r, rt.project, rt.name)
	case http.MethodDelete:
		h.deleteServiceAccount(w, r, rt.name)
	case http.MethodPatch:
		h.updateServiceAccount(w, r, rt.project, rt.name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"unsupported verb on serviceAccount: "+r.Method)
	}
}

func (h *Handler) routeServiceAccountKeys(w http.ResponseWriter, r *http.Request, rt *route) {
	// One-off ":method" calls (disable, enable) are POSTs on a specific key.
	if rt.verb != "" && rt.subName != "" {
		h.routeKeyVerb(w, r, rt)
		return
	}

	switch rt.subName {
	case "":
		switch r.Method {
		case http.MethodPost:
			h.createKey(w, r, rt.project, rt.name)
		case http.MethodGet:
			h.listKeys(w, r, rt.project, rt.name)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
				"unsupported verb on keys collection: "+r.Method)
		}
	default:
		switch r.Method {
		case http.MethodGet:
			h.getKey(w, r, rt.project, rt.name, rt.subName)
		case http.MethodDelete:
			h.deleteKey(w, r, rt.name, rt.subName)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
				"unsupported verb on key: "+r.Method)
		}
	}
}

func (h *Handler) routeRoles(w http.ResponseWriter, r *http.Request, rt *route) {
	// One-off ":method" calls on a specific role (undelete) are POSTs. Route
	// them before the plain method switch, which is otherwise verb-blind.
	if rt.verb != "" && rt.name != "" {
		h.routeRoleVerb(w, r, rt)
		return
	}

	switch rt.name {
	case "":
		switch r.Method {
		case http.MethodPost:
			h.createRole(w, r, rt.project)
		case http.MethodGet:
			h.listRoles(w, r, rt.project)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
				"unsupported verb on roles collection: "+r.Method)
		}
	default:
		switch r.Method {
		case http.MethodGet:
			h.getRole(w, r, rt.project, rt.name)
		case http.MethodDelete:
			h.deleteRole(w, r, rt.project, rt.name)
		case http.MethodPatch:
			h.updateRole(w, r, rt.project, rt.name)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
				"unsupported verb on role: "+r.Method)
		}
	}
}

// routeRoleVerb dispatches one-off ":method" calls on a specific role. The
// only such method is undelete, always a POST.
func (h *Handler) routeRoleVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"method "+rt.verb+" requires POST")

		return
	}

	if rt.verb == undeleteVerb {
		h.undeleteRole(w, r, rt.project, rt.name)

		return
	}

	writeError(w, http.StatusNotFound, "notFound", "unknown method: "+rt.verb)
}
