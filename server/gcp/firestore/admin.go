// This file implements the Firestore Admin API v1 (projects.databases and
// projects.databases.collectionGroups.indexes) as a second server.Handler that
// lives alongside the document data-plane Handler in this package.
//
// Real cloud.google.com/go/firestore/apiv1/admin clients (and the Terraform
// google_firestore_database / google_firestore_index resources) hit this
// handler the same way they hit firestore.googleapis.com:
//
//	POST   /v1/projects/{p}/databases?databaseId={db}                     — create database (LRO)
//	GET    /v1/projects/{p}/databases                                     — list databases
//	GET    /v1/projects/{p}/databases/{db}                                — get database
//	PATCH  /v1/projects/{p}/databases/{db}?updateMask=...                 — patch database (LRO)
//	DELETE /v1/projects/{p}/databases/{db}?etag=...                       — delete database (LRO)
//	GET/DELETE /v1/projects/{p}/databases/{db}/operations/{op}            — poll/delete LRO
//	POST   /v1/projects/{p}/databases/{db}/collectionGroups/{cg}/indexes  — create index (LRO)
//	GET    /v1/projects/{p}/databases/{db}/collectionGroups/{cg}/indexes[/{i}] — list/get index
//	DELETE /v1/projects/{p}/databases/{db}/collectionGroups/{cg}/indexes/{i}   — delete index
//
// Routing note: the document Handler.Matches greedily claims every
// /v1/projects/ path. This admin handler is registered BEFORE it (see
// server/gcp/gcp.go) and its Matches claims ONLY the admin shapes above — every
// path whose segment after databases/{db} is documents (or absent) is left to
// the data-plane handler — so the two coexist on one server without either
// shadowing the other.
package firestore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// Admin API path segments.
const (
	segCollectionGroups = "collectionGroups"
	segIndexes          = "indexes"
	segOperations       = "operations"
)

// versionRetentionDefault is the retention window a database reports when
// point-in-time recovery is disabled (1 hour), matching real Firestore.
const versionRetentionDefault = "3600s"

// dbRecord is the stored metadata for one Firestore database. It carries only
// admin-plane configuration; document data lives in the separate data-plane
// driver and is unaffected by these records.
type dbRecord struct {
	project             string
	databaseID          string
	locationID          string
	dbType              string
	concurrencyMode     string
	appEngineMode       string
	pointInTimeRecovery string
	deleteProtection    string
	uid                 string
	etag                string
	createTime          time.Time
	updateTime          time.Time
}

// operationRec is a completed long-running operation: the typed response a poll
// replays (already carrying its Any @type) plus its typed metadata (some
// clients read the created resource name out of metadata, e.g. the Terraform
// firestore_index resource reads metadata.index), keyed by the operation
// resource name.
type operationRec struct {
	response map[string]any
	metadata map[string]any
}

// AdminHandler serves the Firestore Admin API against in-memory metadata
// stores. It is wire-only (no driver): a database/index resource is a control-
// plane record, so the handler owns its own stores rather than delegating to a
// driver.
type AdminHandler struct {
	databases *memstore.Store[dbRecord]
	indexes   *memstore.Store[indexRecord]
	ops       *memstore.Store[operationRec]

	opSeq atomic.Uint64
}

// NewAdmin returns a Firestore Admin API handler with empty stores.
func NewAdmin() *AdminHandler {
	return &AdminHandler{
		databases: memstore.New[dbRecord](),
		indexes:   memstore.New[indexRecord](),
		ops:       memstore.New[operationRec](),
	}
}

// adminPath is a parsed Firestore Admin URL.
type adminPath struct {
	project      string
	database     string // "" for the databases collection
	operation    string // set for .../operations/{op}
	collGroup    string // set for .../collectionGroups/{cg}
	index        string // set for .../collectionGroups/{cg}/indexes/{i}
	isIndexColl  bool   // .../collectionGroups/{cg}/indexes (collection)
	isDatabase   bool   // .../databases/{db} (resource, no sub-collection)
	isDatabases  bool   // .../databases (collection)
	isOperations bool   // .../operations/{op}
}

// parseAdminPath splits an admin URL into its components. It returns ok=false
// for any shape this handler does not own (notably the data-plane's
// .../documents paths), so Matches can defer those to the document handler.
func parseAdminPath(urlPath string) (adminPath, bool) {
	rest, ok := strings.CutPrefix(urlPath, "/v1/")
	if !ok {
		return adminPath{}, false
	}

	parts := strings.Split(rest, "/")
	// Minimum: projects/{p}/databases
	const minParts = 3
	if len(parts) < minParts || parts[0] != segProjects || parts[2] != segDatabases {
		return adminPath{}, false
	}

	p := adminPath{project: parts[1]}

	switch {
	case len(parts) == minParts: // projects/{p}/databases
		p.isDatabases = true
	case len(parts) == minParts+1: // projects/{p}/databases/{db}
		p.database = parts[3]
		p.isDatabase = true
	default:
		if !parseAdminSubPath(parts, &p) {
			return adminPath{}, false
		}
	}

	return p, true
}

// parseAdminSubPath handles paths below databases/{db}: operations and
// collectionGroups/.../indexes. It returns false for documents (data-plane) and
// any other unrecognized sub-resource.
func parseAdminSubPath(parts []string, p *adminPath) bool {
	p.database = parts[3]

	const (
		idxSub    = 4
		opNameLen = 6 // projects/p/databases/db/operations/op
	)

	switch parts[idxSub] {
	case segOperations:
		if len(parts) != opNameLen {
			return false
		}

		p.operation = parts[5]
		p.isOperations = true

		return true
	case segCollectionGroups:
		return parseIndexPath(parts, p)
	default:
		// documents (data-plane) and anything else: not ours.
		return false
	}
}

// Matches claims exactly the Firestore Admin shapes and defers everything else
// (including the data-plane's .../documents paths) to the document handler.
func (*AdminHandler) Matches(r *http.Request) bool {
	// A colon custom-method suffix (e.g. databases/{db}:exportDocuments,
	// documents:commit) is never an admin CRUD path handled here.
	if strings.Contains(lastSegment(r.URL.Path), ":") {
		return false
	}

	_, ok := parseAdminPath(r.URL.Path)

	return ok
}

// lastSegment returns the final /-delimited segment of a URL path.
func lastSegment(urlPath string) string {
	if i := strings.LastIndex(urlPath, "/"); i >= 0 {
		return urlPath[i+1:]
	}

	return urlPath
}

// ServeHTTP dispatches an admin request by parsed shape and method.
func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parseAdminPath(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown firestore admin path")
		return
	}

	switch {
	case p.isDatabases:
		h.serveDatabasesCollection(w, r, &p)
	case p.isDatabase:
		h.serveDatabaseResource(w, r, &p)
	case p.isOperations:
		h.serveOperation(w, r, &p)
	case p.isIndexColl:
		h.serveIndexCollection(w, r, &p)
	default: // index resource
		h.serveIndexResource(w, r, &p)
	}
}

// serveDatabasesCollection handles the databases collection: POST (create) and
// GET (list).
func (h *AdminHandler) serveDatabasesCollection(w http.ResponseWriter, r *http.Request, p *adminPath) {
	switch r.Method {
	case http.MethodPost:
		h.createDatabase(w, r, p)
	case http.MethodGet:
		h.listDatabases(w, p)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// serveDatabaseResource handles a single database: GET/PATCH/DELETE.
func (h *AdminHandler) serveDatabaseResource(w http.ResponseWriter, r *http.Request, p *adminPath) {
	switch r.Method {
	case http.MethodGet:
		h.getDatabase(w, p)
	case http.MethodPatch:
		h.patchDatabase(w, r, p)
	case http.MethodDelete:
		h.deleteDatabase(w, r, p)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// dbKey is the store key for a database resource name.
func dbKey(project, databaseID string) string {
	return "projects/" + project + "/databases/" + databaseID
}

// createDatabase implements FirestoreAdmin.CreateDatabase (an LRO). The
// databaseId comes from the query string; the request body carries the
// Database configuration.
func (h *AdminHandler) createDatabase(w http.ResponseWriter, r *http.Request, p *adminPath) {
	body, ok := decodeAdminBody(w, r)
	if !ok {
		return
	}

	databaseID := r.URL.Query().Get("databaseId")
	if databaseID == "" {
		databaseID = stringField(body, "name") // fall back to a name segment if present
	}

	if databaseID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "databaseId is required")
		return
	}

	rec, err := buildDBRecord(p.project, databaseID, body)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if !h.databases.SetIfAbsent(dbKey(p.project, databaseID), rec) {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.AlreadyExists, "database %q already exists", databaseID))
		return
	}

	h.writeDoneOperation(w, p.project, databaseID, databaseAnyResponse(&rec))
}

// getDatabase implements FirestoreAdmin.GetDatabase.
func (h *AdminHandler) getDatabase(w http.ResponseWriter, p *adminPath) {
	rec, ok := h.databases.Get(dbKey(p.project, p.database))
	if !ok {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.NotFound, "database %q not found", p.database))
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, renderDatabase(&rec))
}

// listDatabases implements FirestoreAdmin.ListDatabases.
func (h *AdminHandler) listDatabases(w http.ResponseWriter, p *adminPath) {
	prefix := "projects/" + p.project + "/databases/"

	var out []map[string]any

	for _, key := range h.databases.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		if rec, ok := h.databases.Get(key); ok {
			out = append(out, renderDatabase(&rec))
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"databases": out})
}

// patchDatabase implements FirestoreAdmin.UpdateDatabase (an LRO). Fields named
// in updateMask (or, absent a mask, every field present in the body) are
// applied; enum values may arrive as strings or ints.
func (h *AdminHandler) patchDatabase(w http.ResponseWriter, r *http.Request, p *adminPath) {
	body, ok := decodeAdminBody(w, r)
	if !ok {
		return
	}

	mask := splitMask(r.URL.Query().Get("updateMask"))

	key := dbKey(p.project, p.database)

	updated, applyErr := h.applyDatabasePatch(key, body, mask)
	if applyErr != nil {
		gcprest.WriteCErr(w, applyErr)
		return
	}

	h.writeDoneOperation(w, p.project, p.database, databaseAnyResponse(&updated))
}

// applyDatabasePatch mutates the stored record under key per body+mask and
// returns the new record. It is a single store Update so concurrent patches
// don't lose writes.
func (h *AdminHandler) applyDatabasePatch(key string, body map[string]any, mask map[string]bool) (dbRecord, error) {
	current, ok := h.databases.Get(key)
	if !ok {
		return dbRecord{}, cerrors.Newf(cerrors.NotFound, "database %q not found", current.databaseID)
	}

	next, err := patchDBRecord(&current, body, mask)
	if err != nil {
		return dbRecord{}, err
	}

	h.databases.Set(key, next)

	return next, nil
}

// deleteDatabase implements FirestoreAdmin.DeleteDatabase (an LRO). A database
// with delete protection enabled cannot be deleted; a supplied etag must match.
func (h *AdminHandler) deleteDatabase(w http.ResponseWriter, r *http.Request, p *adminPath) {
	key := dbKey(p.project, p.database)

	rec, ok := h.databases.Get(key)
	if !ok {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.NotFound, "database %q not found", p.database))
		return
	}

	if etag := r.URL.Query().Get("etag"); etag != "" && etag != rec.etag {
		gcprest.WriteCErr(w, cerrors.New(cerrors.FailedPrecondition, "etag mismatch"))
		return
	}

	if rec.deleteProtection == deleteProtectionEnabled {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.FailedPrecondition,
			"database %q has delete protection enabled", p.database))

		return
	}

	h.databases.Delete(key)
	// DeleteDatabase's LRO response is the deleted Database.
	h.writeDoneOperation(w, p.project, p.database, databaseAnyResponse(&rec))
}

// serveOperation handles GET (poll) and DELETE on an admin operation name.
func (h *AdminHandler) serveOperation(w http.ResponseWriter, r *http.Request, p *adminPath) {
	name := "projects/" + p.project + "/databases/" + p.database + "/operations/" + p.operation

	switch r.Method {
	case http.MethodGet:
		rec, ok := h.ops.Get(name)
		if !ok {
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "operation "+name+" not found")
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, doneOperationJSON(name, rec.response, rec.metadata))
	case http.MethodDelete:
		if !h.ops.Delete(name) {
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "operation "+name+" not found")
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// writeDoneOperation records a completed operation whose Any response is
// resp (already carrying its @type) and writes the operation envelope. Because
// every CloudEmu mutation completes synchronously the operation is returned
// already done; it is also stored so a client that polls the operation name
// resolves the same done result.
func (h *AdminHandler) writeDoneOperation(w http.ResponseWriter, project, database string, resp map[string]any) {
	h.writeDoneOperationMeta(w, project, database, resp, nil)
}

// writeDoneOperationMeta is writeDoneOperation with typed operation metadata
// (nil when the operation carries none).
func (h *AdminHandler) writeDoneOperationMeta(
	w http.ResponseWriter, project, database string, resp, metadata map[string]any,
) {
	name := "projects/" + project + "/databases/" + database + "/operations/" + h.nextOpID()
	h.ops.Set(name, operationRec{response: resp, metadata: metadata})
	gcprest.WriteJSON(w, http.StatusOK, doneOperationJSON(name, resp, metadata))
}

// nextOpID returns a unique operation id.
func (h *AdminHandler) nextOpID() string {
	return strconv.FormatUint(h.opSeq.Add(1), 10) + "-" + randHex()
}

// doneOperationJSON builds a completed google.longrunning.Operation envelope.
func doneOperationJSON(name string, response, metadata map[string]any) map[string]any {
	out := map[string]any{
		"name":     name,
		"done":     true,
		"response": response,
	}

	if metadata != nil {
		out["metadata"] = metadata
	}

	return out
}

// decodeAdminBody reads a JSON object body into a generic map. An empty body is
// treated as an empty object (create/patch may legitimately omit optional
// fields). It writes a 400 and returns ok=false on malformed JSON.
func decodeAdminBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var body map[string]any

	dec := json.NewDecoder(r.Body)

	if err := dec.Decode(&body); err != nil {
		if err.Error() == "EOF" {
			return map[string]any{}, true
		}

		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())

		return nil, false
	}

	if body == nil {
		body = map[string]any{}
	}

	return body, true
}

// splitMask parses a comma-separated updateMask into a set. An empty mask
// returns nil, which patch treats as "apply every field present in the body".
func splitMask(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	out := map[string]bool{}

	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out[f] = true
		}
	}

	return out
}

// randHex returns 8 random hex characters for operation/uid suffixes.
func randHex() string {
	var b [4]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}

	return hex.EncodeToString(b[:])
}
