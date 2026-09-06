package driver

// AppSync exception names. These select the precise X-Amzn-Errortype at the
// wire layer for exceptions that don't map one-to-one to canonical cloudemu
// codes.
const (
	ExNotFound        = "NotFoundException"
	ExBadRequest      = "BadRequestException"
	ExConcurrentMod   = "ConcurrentModificationException"
	ExAPIKeyValidity  = "ApiKeyValidityOutOfBoundsException"
	ExInternalFailure = "InternalFailureException"
)

// APIError tags a canonical cloudemu error with the AppSync exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
