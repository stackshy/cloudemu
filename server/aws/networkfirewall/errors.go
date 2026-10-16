package networkfirewall

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// writeErr renders err as the Network Firewall exception it represents: a typed
// driver error carries its exact exception name; everything else maps by code.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	if ex, ok := nfdriver.ExceptionOf(err); ok {
		wire.WriteJSONError(w, http.StatusBadRequest, ex, msg)
		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, nfdriver.ExInvalidOperation, msg)
	case cerrors.IsAlreadyExists(err), cerrors.IsInvalidArgument(err):
		// A duplicate name is a request error: InvalidRequestException is the one
		// exception every Create operation models.
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", msg)
	}
}

// capability returns the optional operation family T the driver implements, or
// writes UnsupportedOperationException and returns false.
func capability[T any](h *Handler, w http.ResponseWriter) (T, bool) {
	c, ok := h.db.(T)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest, nfdriver.ExUnsupportedOperation,
			"the operation is not supported by this Network Firewall implementation")
	}

	return c, ok
}
