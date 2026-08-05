package route53resolver

import (
	stderrors "errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// writeErr maps a canonical cloudemu error to the closest Route 53 Resolver
// exception. The service uses AWS JSON 1.1, so the SDK keys off the "__type"
// body written by wire.WriteJSONError to select the typed error.
func writeErr(w http.ResponseWriter, err error) {
	msg := wireMessage(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServiceErrorException", msg)
	}
}

// wireMessage returns the bare error message, stripping the internal
// "<Code>: " prefix that cerrors.Error.Error() adds, so the SDK-surfaced
// message reads like real AWS.
func wireMessage(err error) string {
	var ce *cerrors.Error
	if stderrors.As(err, &ce) {
		return ce.Message
	}

	return err.Error()
}
