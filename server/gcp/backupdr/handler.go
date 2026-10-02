// Package backupdr implements the Google Cloud Backup and DR backup vault control
// plane (backupdr.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/backupdr/v1 clients, gcloud, and the Terraform google
// provider's google_backup_dr_backup_vault resource hit this handler unchanged.
//
// Coverage (backup vault control plane):
//
//	POST   /v1/…/backupVaults?backupVaultId=      : CreateBackupVault (LRO)
//	GET    /v1/…/backupVaults                     : ListBackupVaults
//	GET    /v1/…/backupVaults/{id}                : GetBackupVault
//	PATCH  /v1/…/backupVaults/{id}?updateMask=    : UpdateBackupVault (LRO)
//	DELETE /v1/…/backupVaults/{id}                : DeleteBackupVault (LRO)
//	GET    /v1/…/operations/{op}                  : Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and,
// for create/patch, the resulting vault embedded in `response` as an Any typed
// type.googleapis.com/google.cloud.backupdr.v1.BackupVault, so an SDK or
// Terraform LRO wait terminates on the first poll.
//
// Location-scoped operations: a vault's operations live under
// /v1/projects/{p}/locations/{l}/operations, the same space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The backupVaults resource-type guard keeps this handler
// disjoint from every other /v1/projects/ handler.
//
// Known gap (not implemented): backupPlans, backupPlanAssociations,
// dataSources, backups and managementServers. This handler does not claim
// those paths, so a vault is never protected by a backup-plan reference
// (ignoreBackupPlanReferences on delete has nothing to check). Note that
// /v1/projects/{p}/locations/{l}/backupPlans requests are currently answered
// by the GKE Backup handler (server/gcp/gkebackup), which shares that path
// shape and replies with a google.cloud.gkebackup.v1.BackupPlan; a Backup and
// DR backup plan (e.g. Terraform google_backup_dr_backup_plan) therefore lands
// in GKE Backup state rather than being rejected.
package backupdr

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	vaultsColl       = "backupVaults"
	vaultIDParam     = "backupVaultId"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	vaultTypeURL = "type.googleapis.com/google.cloud.backupdr.v1.BackupVault"
)

// Handler serves backupdr.googleapis.com v1 requests against a BackupDR driver.
type Handler struct {
	db bdrdriver.BackupDR

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Backup and DR handler backed by db.
func New(db bdrdriver.BackupDR) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Backup and DR v1 path.
type route struct {
	project  string
	location string
	resource string // "backupVaults" | "operations"
	name     string // vault id or operation id; empty for the collection
}

// parseRoute extracts the components of a Backup and DR v1 path. It recognizes
// only the backupVaults and operations resources under a locations scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rest := parts[minResourceParts:]
	if len(rest) == 0 || len(rest) > itemParts || !knownResource(rest[0]) {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3], resource: rest[0]}
	if len(rest) == itemParts {
		rt.name = rest[1]
	}

	return rt, true
}

// knownResource reports whether seg is a resource collection this handler serves.
func knownResource(seg string) bool {
	return seg == vaultsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{backupVaults|operations}[/…]
// paths. An operations path is claimed only when this handler has no shared LRO
// registry (a standalone package server); in an assembled server the shared
// poller owns it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.resource == operationsSeg && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Backup and DR path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt)
		return
	}

	h.serveItem(w, r, rt)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createVault(w, r, rt)
	case http.MethodGet:
		h.listVaults(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getVault(w, r, rt)
	case http.MethodPatch:
		h.patchVault(w, r, rt)
	case http.MethodDelete:
		h.deleteVault(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full backup vault resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + vaultsColl + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
