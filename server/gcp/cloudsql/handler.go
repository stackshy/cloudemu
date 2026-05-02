// Package cloudsql implements the GCP Cloud SQL Admin REST API as a
// server.Handler. Real google.golang.org/api/sqladmin/v1 clients configured
// with a custom endpoint hit this handler the same way they hit
// sqladmin.googleapis.com.
//
// MVP coverage:
//
//	POST   /v1/projects/{p}/instances                            — Insert
//	GET    /v1/projects/{p}/instances                            — List
//	GET    /v1/projects/{p}/instances/{i}                        — Get
//	PATCH  /v1/projects/{p}/instances/{i}                        — Patch
//	PUT    /v1/projects/{p}/instances/{i}                        — Update
//	DELETE /v1/projects/{p}/instances/{i}                        — Delete
//	POST   /v1/projects/{p}/instances/{i}/restart                — Restart
//	POST   /v1/projects/{p}/instances/{i}/restoreBackup          — Restore from backup
//	POST   /v1/projects/{p}/instances/{i}/backupRuns             — Create backup run
//	GET    /v1/projects/{p}/instances/{i}/backupRuns             — List backup runs
//	GET    /v1/projects/{p}/instances/{i}/backupRuns/{id}        — Get backup run
//	DELETE /v1/projects/{p}/instances/{i}/backupRuns/{id}        — Delete backup run
//	GET    /v1/projects/{p}/operations/{op}                      — Poll operation (always DONE)
//
// All mutating endpoints return Operation envelopes with status=DONE so SDK
// pollers terminate on the first response. Start/Stop are emulated via
// Patch with settings.activationPolicy=ALWAYS|NEVER, matching real Cloud SQL.
//
// The /v1/projects/ prefix is shared with Cloud Functions, Pub/Sub, and
// Firestore. Matches narrows by the third path segment so dispatch stays
// unambiguous: it only claims "instances" or "operations" — anything else
// (locations, topics, subscriptions, databases) falls through.
package cloudsql

import (
	"net/http"
	"strings"

	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20

	resourceInstances  = "instances"
	resourceOperations = "operations"
	resourceBackupRuns = "backupRuns"
)

// Handler serves Cloud SQL Admin REST requests against a relationaldb driver.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns a Cloud SQL handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches accepts /v1/projects/{p}/{instances|operations}/... paths.
// Other resource types under /v1/projects/ (locations, topics, subscriptions,
// databases) belong to Cloud Functions, Pub/Sub, or Firestore respectively
// and must fall through.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")

	const idxResource = 1

	if len(parts) <= idxResource {
		return false
	}

	switch parts[idxResource] {
	case resourceInstances, resourceOperations:
		return true
	}

	return false
}

// path components parsed out of the URL.
//
//	/sql/v1beta4/projects/{p}/instances
//	/sql/v1beta4/projects/{p}/instances/{i}
//	/sql/v1beta4/projects/{p}/instances/{i}/{action}
//	/sql/v1beta4/projects/{p}/instances/{i}/backupRuns[/{id}]
//	/sql/v1beta4/projects/{p}/operations/{op}
type sqlPath struct {
	project     string
	resource    string // "instances" or "operations"
	name        string // instance or operation name; empty for collection
	subResource string // "backupRuns" when resource is instances and a sub-collection
	subName     string // backup run id
	action      string // "restart", "restoreBackup", etc.
}

// parsePath splits the URL into our parts struct. Returns ok=false if the
// path is malformed.
func parsePath(urlPath string) (sqlPath, bool) {
	const (
		minParts       = 2
		idxName        = 2
		idxSubResource = 3
		idxSubName     = 4
	)

	rest := strings.TrimPrefix(urlPath, pathPrefix)

	parts := strings.Split(rest, "/")
	if len(parts) < minParts {
		return sqlPath{}, false
	}

	out := sqlPath{project: parts[0], resource: parts[1]}

	if len(parts) > idxName {
		out.name = parts[idxName]
	}

	if len(parts) > idxSubResource {
		// {action} OR {subResource}
		seg := parts[idxSubResource]
		if seg == resourceBackupRuns {
			out.subResource = seg
		} else {
			out.action = seg
		}
	}

	if len(parts) > idxSubName && out.subResource != "" {
		out.subName = parts[idxSubName]
	}

	return out, true
}

// ServeHTTP routes the parsed path to the matching operation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "malformed path")
		return
	}

	switch p.resource {
	case resourceOperations:
		h.serveOperation(w, r, &p)
	case resourceInstances:
		h.serveInstancesRoute(w, r, &p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported resource: "+p.resource)
	}
}

func (h *Handler) serveInstancesRoute(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	// Sub-resource: backup runs.
	if p.subResource == resourceBackupRuns {
		h.serveBackupRunsRoute(w, r, p)
		return
	}

	// Action on an instance: restart, restoreBackup.
	if p.action != "" {
		h.serveInstanceAction(w, r, p)
		return
	}

	// Single instance.
	if p.name != "" {
		h.serveInstance(w, r, p)
		return
	}

	// Collection.
	h.serveInstanceCollection(w, r, p)
}

func (h *Handler) serveInstanceCollection(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	switch r.Method {
	case http.MethodPost:
		h.insertInstance(w, r, p)
	case http.MethodGet:
		h.listInstances(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveInstance(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, p)
	case http.MethodPatch, http.MethodPut:
		h.patchInstance(w, r, p)
	case http.MethodDelete:
		h.deleteInstance(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveInstanceAction(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	switch p.action {
	case "restart":
		h.restartInstance(w, r, p)
	case "restoreBackup":
		h.restoreInstance(w, r, p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported action: "+p.action)
	}
}

func (h *Handler) serveBackupRunsRoute(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if p.subName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertBackupRun(w, r, p)
		case http.MethodGet:
			h.listBackupRuns(w, r, p)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackupRun(w, r, p)
	case http.MethodDelete:
		h.deleteBackupRun(w, r, p)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request, p *sqlPath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	if p.name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "operation name required")
		return
	}

	writeJSON(w, http.StatusOK, doneOperation(p.project, p.name, "GET", "noop"))
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}
