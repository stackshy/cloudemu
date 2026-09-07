package driver

// AWS Backup exception names. These select the precise X-Amzn-Errortype header
// at the wire layer so the real SDK decodes the matching typed exception.
const (
	ExResourceNotFound = "ResourceNotFoundException"
	ExAlreadyExists    = "AlreadyExistsException"
	ExInvalidParameter = "InvalidParameterValueException"
	ExInvalidRequest   = "InvalidRequestException"
	ExMissingParameter = "MissingParameterValueException"
	ExLimitExceeded    = "LimitExceededException"
	ExServiceUnavail   = "ServiceUnavailableException"
)

// APIError tags a canonical cloudemu error with the AWS Backup exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical code still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
