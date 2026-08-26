package efs

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception, falling back to the body's __type.
// FileSystemId is set on a FileSystemAlreadyExists error so the SDK exception's
// FileSystemId member is populated for idempotent CreateFileSystem retries.
type errorBody struct {
	Type         string `json:"__type"`
	ErrorCode    string `json:"ErrorCode"`
	Message      string `json:"Message"`
	FileSystemID string `json:"FileSystemId,omitempty"`
}

// genericErrorType is the X-Amzn-Errortype EFS uses for protocol-level errors
// (bad path, method, JSON body, pagination token) that carry no per-resource
// typed exception.
const genericErrorType = "BadRequest"

// writeError writes a restJson1 error response for a protocol-level fault. EFS
// error bodies carry both ErrorCode and Message; the header selects the typed
// exception (always BadRequest here).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeErrorBody(w, status, genericErrorType,
		errorBody{Type: genericErrorType, ErrorCode: genericErrorType, Message: msg})
}

// writeErrorBody writes a restJson1 error response from a fully-built body,
// letting callers attach extra members (e.g. FileSystemId).
func writeErrorBody(w http.ResponseWriter, status int, errType string, body errorBody) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}

// exceptionFor returns the EFS X-Amzn-Errortype for a resource kind + canonical
// code. EFS has per-resource typed exceptions, so a NotFound on a mount target
// must surface as MountTargetNotFound, not FileSystemNotFound. Errors carry the
// kind via driver.ResourceError; untagged errors fall back to the file-system
// exception.
func exceptionFor(kind string, err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		byKind := map[string]string{
			driver.KindMountTarget: "MountTargetNotFound",
			driver.KindAccessPoint: "AccessPointNotFound",
			driver.KindPolicy:      "PolicyNotFound",
			driver.KindReplication: "ReplicationNotFound",
		}
		if t, ok := byKind[kind]; ok {
			return http.StatusNotFound, t
		}

		return http.StatusNotFound, "FileSystemNotFound"
	case cerrors.IsAlreadyExists(err):
		byKind := map[string]string{
			driver.KindMountTarget: "MountTargetConflict",
			driver.KindReplication: "ReplicationAlreadyExists",
		}
		if t, ok := byKind[kind]; ok {
			return http.StatusConflict, t
		}

		return http.StatusConflict, "FileSystemAlreadyExists"
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, "BadRequest"
	case cerrors.IsFailedPrecondition(err):
		return http.StatusConflict, "FileSystemInUse"
	default:
		return http.StatusInternalServerError, "InternalServerError"
	}
}

// writeErr maps a canonical cloudemu error to the precise EFS exception, using
// the resource kind carried by driver.ResourceError when present.
func writeErr(w http.ResponseWriter, err error) {
	kind := ""
	resourceID := ""

	var re *driver.ResourceError
	if errors.As(err, &re) {
		kind = re.Kind
		resourceID = re.ResourceID
	}

	status, errType := exceptionFor(kind, err)

	body := errorBody{Type: errType, ErrorCode: errType, Message: err.Error()}
	if kind == driver.KindFileSystem {
		body.FileSystemID = resourceID
	}

	writeErrorBody(w, status, errType, body)
}

func notFound(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// decodeJSON decodes the request body into v. An empty body is treated as an
// empty object (several EFS ops carry all input in the path).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if err.Error() == "EOF" {
			return true
		}

		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	encodeJSON(w, v)
}

// encodeJSON writes v as JSON without touching status/headers, for callers that
// have already written a non-200 success status (e.g. 201 Created).
func encodeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// writeStatus writes an empty body with a specific status (e.g. 204 for
// CreateMountTarget-less deletes). Most EFS deletes return 204 No Content.
func writeStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}
