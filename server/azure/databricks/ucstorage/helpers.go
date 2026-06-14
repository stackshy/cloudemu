package ucstorage

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// splitPath strips surrounding slashes and returns the path segments.
// Path /api/2.1/unity-catalog/metastores/abc →
// [api, 2.1, unity-catalog, metastores, abc].
func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "/")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, codeMalformed, "invalid JSON: "+err.Error())

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

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{ErrorCode: code, Message: msg})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, codeInvalidParam, "method not allowed")
}

func notFound(w http.ResponseWriter, kind, name string) {
	writeError(w, http.StatusNotFound, codeNotFound, kind+" does not exist: "+name)
}

func missingField(w http.ResponseWriter, field string) {
	writeError(w, http.StatusBadRequest, codeInvalidParam, field+" is required")
}

// updateSpec describes how to apply a PATCH to a name-keyed resource stored in
// store. apply mutates the resource from the decoded request and reports the
// requested rename ("" for no change); view renders the response body. The
// shared flow handles locking, lookup, re-keying, and the updated_at stamp.
type updateSpec[R any, V any] struct {
	kind  string
	store map[string]*V
	apply func(*V, *R) string
	setAt func(*V, int64)
	view  func(*V) any
}

// updateResource runs the shared decode/lock/lookup/apply/rename/respond flow
// used by the name-keyed PATCH handlers.
func updateResource[R any, V any](h *Handler, w http.ResponseWriter, r *http.Request, key string, spec updateSpec[R, V]) {
	var in R
	if !decode(w, r, &in) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	item, ok := spec.store[key]
	if !ok {
		notFound(w, spec.kind, key)

		return
	}

	if n := spec.apply(item, &in); n != "" && n != key {
		delete(spec.store, key)
		spec.store[n] = item
	}

	spec.setAt(item, time.Now().UnixMilli())

	writeJSON(w, spec.view(item))
}
