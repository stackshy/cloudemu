package driver

// FIS exception names. These select the precise X-Amzn-Errortype at the wire
// layer.
const (
	ExNotFound   = "ResourceNotFoundException"
	ExValidation = "ValidationException"
	ExConflict   = "ConflictException"
	ExQuota      = "ServiceQuotaExceededException"
)

// APIError tags a canonical cloudemu error with the FIS exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical code still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
