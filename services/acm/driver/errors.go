package driver

// ACM exception names, used to select the precise X-Amzn-Errortype at the wire
// layer. ACM models several distinct exceptions (InvalidArnException,
// InvalidParameterException, …) that don't map one-to-one to canonical codes, so
// errors carry the exception they concern.
const (
	ExInvalidArn       = "InvalidArnException"
	ExInvalidParameter = "InvalidParameterException"
	ExInvalidState     = "InvalidStateException"
	ExTooManyTags      = "TooManyTagsException"
)

// APIError tags a canonical cloudemu error with the ACM exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
