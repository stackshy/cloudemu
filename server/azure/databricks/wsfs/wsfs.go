// Package wsfs implements the Databricks Workspace (notebooks/directories)
// data-plane REST API (the /api/2.0/workspace surface) as a self-contained
// server.Handler backed by in-memory state. Point the real
// github.com/databricks/databricks-sdk-go WorkspaceClient at a server.Server
// registered with a Handler from this package and w.Workspace import, export,
// list, mkdirs, delete, and get-status work end-to-end.
//
// Covered endpoints:
//
//	POST /api/2.0/workspace/import
//	GET  /api/2.0/workspace/export
//	GET  /api/2.0/workspace/list
//	POST /api/2.0/workspace/mkdirs
//	POST /api/2.0/workspace/delete
//	GET  /api/2.0/workspace/get-status
package wsfs

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

const wsfsMaxBodyBytes = 11 << 20

// Workspace object types mirrored from the Databricks SDK enum.
const (
	objectNotebook  = "NOTEBOOK"
	objectDirectory = "DIRECTORY"
)

// Default notebook language when an import omits one.
const defaultLanguage = "PYTHON"

// minPathSegs is the [api, ver, workspace, action] segment count after split.
const minPathSegs = 4

// object is a stored workspace entry.
type object struct {
	content    []byte
	objectType string
	language   string
	objectID   int64
}

// Handler serves the Databricks workspace filesystem data-plane API.
type Handler struct {
	mu      sync.RWMutex
	objects map[string]*object
	nextID  int64
}

// New returns a workspace handler with an empty in-memory tree containing the
// implicit root directory.
func New() *Handler {
	h := &Handler{objects: make(map[string]*object)}
	h.nextID++
	h.objects["/"] = &object{objectType: objectDirectory, objectID: h.nextID}

	return h
}

// Matches claims /api/{ver}/workspace/... paths.
func (*Handler) Matches(r *http.Request) bool {
	parts := wsfsSplit(r.URL.Path)
	if len(parts) < minPathSegs || parts[0] != "api" {
		return false
	}

	return parts[2] == "workspace"
}

// ServeHTTP routes by the action segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := wsfsSplit(r.URL.Path)
	if len(parts) < minPathSegs {
		wsfsError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unsupported path")

		return
	}

	switch parts[3] {
	case "import":
		h.serveImport(w, r)
	case "export":
		h.serveExport(w, r)
	case "list":
		h.serveList(w, r)
	case "mkdirs":
		h.serveMkdirs(w, r)
	case "delete":
		h.serveDelete(w, r)
	case "get-status":
		h.serveGetStatus(w, r)
	default:
		wsfsError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unknown action: "+parts[3])
	}
}

// wsfsSplit strips surrounding slashes and returns the path segments, keeping
// the leading "api" at index 0 for the Matches/ServeHTTP guards.
func wsfsSplit(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func wsfsDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, wsfsMaxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		wsfsError(w, http.StatusBadRequest, "MALFORMED_REQUEST", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func wsfsJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

// wsfsErrorBody is the Databricks error envelope shape.
type wsfsErrorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func wsfsError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(wsfsErrorBody{ErrorCode: code, Message: msg})
}

func wsfsMethodNotAllowed(w http.ResponseWriter) {
	wsfsError(w, http.StatusMethodNotAllowed, "INVALID_PARAMETER_VALUE", "method not allowed")
}

// normalizePath trims trailing slashes (except for root) so paths compare
// consistently regardless of how the caller spells them.
func normalizePath(p string) string {
	if p == "" {
		return ""
	}

	if p == "/" {
		return "/"
	}

	return strings.TrimRight(p, "/")
}

// parentOf returns the parent directory path of p ("/" for top-level entries).
func parentOf(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return "/"
	}

	return p[:idx]
}
