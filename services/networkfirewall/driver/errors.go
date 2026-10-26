package driver

import (
	"errors"
	"fmt"
)

// Network Firewall exception names, as they appear in the "__type" member of an
// AWS JSON 1.0 error. The wire handler renders these verbatim, so the SDK
// surfaces the exact typed exception a real firewall returns.
const (
	ExInvalidOperation      = "InvalidOperationException"
	ExInvalidToken          = "InvalidTokenException"
	ExInvalidResourcePolicy = "InvalidResourcePolicyException"
	ExUnsupportedOperation  = "UnsupportedOperationException"
)

// APIError tags an error with the Network Firewall exception it must surface
// as. Errors without a tag fall back to the generic code mapping in the wire
// handler (NotFound -> ResourceNotFoundException and so on).
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }

func newAPIError(exception, format string, args ...any) error {
	return &APIError{Exception: exception, Err: fmt.Errorf(format, args...)} //nolint:err113 // typed API error carrying a formatted message
}

// InvalidOperation returns an InvalidOperationException: the request is valid
// but not allowed in the resource's current state (delete protection, a
// resource that is still in use, change protection).
func InvalidOperation(format string, args ...any) error {
	return newAPIError(ExInvalidOperation, format, args...)
}

// InvalidToken returns an InvalidTokenException: the update token is stale.
func InvalidToken(format string, args ...any) error {
	return newAPIError(ExInvalidToken, format, args...)
}

// InvalidResourcePolicy returns an InvalidResourcePolicyException: a resource
// policy statement failed validation.
func InvalidResourcePolicy(format string, args ...any) error {
	return newAPIError(ExInvalidResourcePolicy, format, args...)
}

// ExceptionOf returns the exception tagged on err and true, or "" and false.
func ExceptionOf(err error) (string, bool) {
	var api *APIError
	if errors.As(err, &api) {
		return api.Exception, true
	}

	return "", false
}
