package driver

// Transfer Family exception names, used to select the precise __type at the
// wire layer. These mirror the typed exceptions the
// aws-sdk-go-v2/service/transfer client models so callers can errors.As them.
const (
	ExResourceNotFound = "ResourceNotFoundException"
	ExResourceExists   = "ResourceExistsException"
	ExInvalidRequest   = "InvalidRequestException"
	ExThrottling       = "ThrottlingException"
	ExInternalService  = "InternalServiceError"
)

// APIError tags a canonical cloudemu error with the Transfer exception name it
// concerns, so the server can emit the right __type while GetCode still
// resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
