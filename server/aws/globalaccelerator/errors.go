package globalaccelerator

import (
	"encoding/json"
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	gadriver "github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// contentTypeJSON11 is the AWS JSON 1.1 media type Global Accelerator speaks.
const contentTypeJSON11 = "application/x-amz-json-1.1"

// writeJSON writes a 200 AWS JSON 1.1 success body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON11)
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes an AWS JSON 1.1 error response. The SDK reads the __type
// member (and X-Amzn-Errortype header) to select a typed exception.
func writeError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON11)
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  errType,
		"Message": msg,
	})
}

// statusForException returns the HTTP status the real API pairs with a Global
// Accelerator exception name. Every documented client exception is a 400 except
// InternalServiceErrorException.
func statusForException(exception string) int {
	if exception == gadriver.ExInternalService {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}

// exceptionForCode maps a canonical cloudemu code to a Global Accelerator
// exception name for errors that did not carry an explicit driver.APIError tag.
func exceptionForCode(err error) (status int, errType string) {
	switch {
	case cerrors.IsNotFound(err):
		return http.StatusBadRequest, gadriver.ExAcceleratorNotFound
	case cerrors.IsFailedPrecondition(err):
		return http.StatusBadRequest, gadriver.ExInvalidArgument
	case cerrors.IsInvalidArgument(err):
		return http.StatusBadRequest, gadriver.ExInvalidArgument
	default:
		return http.StatusInternalServerError, gadriver.ExInternalService
	}
}

// writeErr maps a driver error to the precise Global Accelerator exception.
// Tagged driver.APIError values are honored first so the exact exception name is
// preserved; untagged errors fall back to the canonical-code mapping.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	var apiErr *gadriver.APIError
	if errors.As(err, &apiErr) {
		writeError(w, statusForException(apiErr.Exception), apiErr.Exception, msg)

		return
	}

	status, errType := exceptionForCode(err)
	writeError(w, status, errType, msg)
}
