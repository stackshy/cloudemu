package driver

// EventBridge Scheduler exception names. These select the precise
// X-Amzn-Errortype at the wire layer for exceptions that do not map one-to-one
// to canonical cloudemu codes.
const (
	ExResourceNotFound     = "ResourceNotFoundException"
	ExValidation           = "ValidationException"
	ExConflict             = "ConflictException"
	ExServiceQuotaExceeded = "ServiceQuotaExceededException"
	ExThrottling           = "ThrottlingException"
	ExInternalServer       = "InternalServerException"
)

// APIError tags a canonical cloudemu error with the Scheduler exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical code still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
