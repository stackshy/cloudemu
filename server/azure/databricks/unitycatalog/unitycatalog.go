// Package unitycatalog emulates the Databricks Unity Catalog core REST API
// (the /api/2.1/unity-catalog/{catalogs,schemas,tables} surface served at the
// workspace URL) as a server.Handler. Point the real
// github.com/databricks/databricks-sdk-go WorkspaceClient at a server
// registered with this handler and w.Catalogs, w.Schemas, and w.Tables work
// end-to-end against an in-memory backend.
//
// Covered endpoints:
//
//	POST   /api/2.1/unity-catalog/catalogs              create
//	GET    /api/2.1/unity-catalog/catalogs              list
//	GET    /api/2.1/unity-catalog/catalogs/{name}       get
//	PATCH  /api/2.1/unity-catalog/catalogs/{name}       update
//	DELETE /api/2.1/unity-catalog/catalogs/{name}       delete
//	POST   /api/2.1/unity-catalog/schemas               create
//	GET    /api/2.1/unity-catalog/schemas               list
//	GET    /api/2.1/unity-catalog/schemas/{full_name}   get
//	PATCH  /api/2.1/unity-catalog/schemas/{full_name}   update
//	DELETE /api/2.1/unity-catalog/schemas/{full_name}   delete
//	POST   /api/2.1/unity-catalog/tables                create
//	GET    /api/2.1/unity-catalog/tables                list
//	GET    /api/2.1/unity-catalog/tables/{full_name}    get
//	DELETE /api/2.1/unity-catalog/tables/{full_name}    delete
package unitycatalog

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

const maxBodyBytes = 5 << 20

// resource path segments under "unity-catalog".
const (
	resCatalogs = "catalogs"
	resSchemas  = "schemas"
	resTables   = "tables"
)

// ucSegs is the [api, ver, unity-catalog, resource] segment count; an item
// path adds the trailing {name}/{full_name} segment for ucItemSegs.
const (
	ucSegs     = 4
	ucItemSegs = 5
	segUnity   = "unity-catalog"
)

// Databricks error codes.
const (
	codeNotFound      = "RESOURCE_DOES_NOT_EXIST"
	codeAlreadyExists = "RESOURCE_ALREADY_EXISTS"
	codeInvalidParam  = "INVALID_PARAMETER_VALUE"
	codeMalformed     = "MALFORMED_REQUEST"
	codeNotFoundPath  = "ENDPOINT_NOT_FOUND"
)

// catalog is the in-memory state for a single catalog.
type catalog struct {
	name    string
	comment string
}

// schema is the in-memory state for a single schema, keyed by "catalog.schema".
type schema struct {
	name        string
	catalogName string
	comment     string
}

// table is the in-memory state for a single table, keyed by
// "catalog.schema.table".
type table struct {
	name        string
	catalogName string
	schemaName  string
	comment     string
}

// Handler serves the Unity Catalog core REST API backed by in-memory maps.
// It is safe for concurrent use.
type Handler struct {
	mu       sync.RWMutex
	catalogs map[string]*catalog
	schemas  map[string]*schema
	tables   map[string]*table
}

// New returns a Unity Catalog handler with empty in-memory state.
func New() *Handler {
	return &Handler{
		catalogs: make(map[string]*catalog),
		schemas:  make(map[string]*schema),
		tables:   make(map[string]*table),
	}
}

// Matches claims /api/{ver}/unity-catalog/{catalogs,schemas,tables}/... paths.
// It deliberately does not claim metastores, external-locations,
// storage-credentials, or volumes.
func (*Handler) Matches(r *http.Request) bool {
	parts := split(r.URL.Path)
	if len(parts) < ucSegs || parts[0] != "api" || parts[2] != segUnity {
		return false
	}

	switch parts[3] {
	case resCatalogs, resSchemas, resTables:
		return true
	default:
		return false
	}
}

// ServeHTTP routes by the resource segment after "unity-catalog".
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := split(r.URL.Path)
	if len(parts) < ucSegs {
		writeErr(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	name := itemName(parts)

	switch parts[3] {
	case resCatalogs:
		h.serveCatalogs(w, r, name)
	case resSchemas:
		h.serveSchemas(w, r, name)
	case resTables:
		h.serveTables(w, r, name)
	default:
		writeErr(w, http.StatusNotFound, codeNotFoundPath, "unknown resource: "+parts[3])
	}
}

// itemName joins any path segments after the resource into a single name,
// preserving dotted full names that were not percent-encoded into one segment.
func itemName(parts []string) string {
	if len(parts) < ucItemSegs {
		return ""
	}

	return strings.Join(parts[4:], "/")
}

// split strips surrounding slashes and returns the path segments, keeping
// "api" at index 0 for the Matches/ServeHTTP guards.
func split(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, codeMalformed, "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the Databricks error envelope shape.
type errorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeErr(w, http.StatusMethodNotAllowed, codeInvalidParam, "method not allowed")
}
