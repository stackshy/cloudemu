package driver

// App Runner modeled exception names, used to select the precise __type at the
// wire layer. App Runner speaks AWS JSON 1.0 and models a small, fixed set of
// exceptions; errors carry the exception they concern so the server emits the
// right __type while GetCode still resolves the HTTP status through Unwrap.
const (
	ExInternalServiceError = "InternalServiceErrorException"
	ExInvalidRequest       = "InvalidRequestException"
	ExInvalidState         = "InvalidStateException"
	ExResourceNotFound     = "ResourceNotFoundException"
)

// APIError tags a canonical cloudemu error with the App Runner exception name it
// concerns, so the server can emit the right __type while GetCode still resolves
// the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
