package sesv2

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sesdriver "github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// errorBody is the restJson1 error body. The SDK reads the X-Amzn-Errortype
// header to select a typed exception, falling back to the body's __type.
type errorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// writeError writes a restJson1 error response.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorBody{Type: errType, Message: msg})
}

// exceptionFor maps a canonical cloudemu error to the SES v2 typed exception.
func exceptionFor(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusNotFound, "NotFoundException"
	case cerrors.IsAlreadyExists(err):
		return http.StatusBadRequest, "AlreadyExistsException"
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, "BadRequestException"
	case cerrors.IsFailedPrecondition(err):
		return http.StatusConflict, "ConflictException"
	case cerrors.IsThrottled(err):
		return http.StatusTooManyRequests, "TooManyRequestsException"
	default:
		return http.StatusInternalServerError, "InternalServiceErrorException"
	}
}

// writeErr maps a canonical cloudemu error to the precise SES v2 exception,
// honoring a tagged driver.APIError exception (e.g. MessageRejected) when present.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *sesdriver.APIError
	if errors.As(err, &apiErr) {
		writeError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	status, errType := exceptionFor(err)
	writeError(w, status, errType, err.Error())
}

func notFound(w http.ResponseWriter, path string) {
	writeError(w, http.StatusNotFound, "BadRequestException", "unsupported path: "+path)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "BadRequestException", "method not allowed")
}

// decodeJSON decodes the request body into v. An empty body is treated as an
// empty object (several SES ops carry all input in the path).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if err.Error() == "EOF" {
			return true
		}

		writeError(w, http.StatusBadRequest, "BadRequestException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// writeOK writes an empty success body, or the mapped error if err is non-nil.
func writeOK(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// writeJSON writes a 200 restJson1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	encodeJSON(w, v)
}

// encodeJSON writes v as JSON without touching status/headers.
func encodeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}
