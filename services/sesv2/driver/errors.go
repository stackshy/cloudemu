package driver

// SES v2 exception names that don't map one-to-one to canonical codes, carried
// explicitly so the wire layer can emit the precise X-Amzn-Errortype.
const (
	ExMessageRejected = "MessageRejected"
)

// APIError tags a canonical cloudemu error with the SES v2 exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
