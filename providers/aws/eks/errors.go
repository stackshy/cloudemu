package eks

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// apiError pairs a canonical cloudemu error with the precise EKS exception name
// and HTTP status the server should surface. It unwraps to the underlying
// *cerrors.Error so the cerrors.IsX predicates keep classifying it, while
// exposing EKSException() for the server's error mapper.
//
// It is used where the generic code-based mapping would surface the wrong
// exception: deleting a cluster that still has managed node groups, Fargate
// profiles, or add-ons attached is a ResourceInUseException with HTTP 409, not
// the InvalidRequestException (400) a bare FailedPrecondition maps to.
type apiError struct {
	err       *cerrors.Error
	exception string
	status    int
}

// Error implements the error interface.
func (e *apiError) Error() string { return e.err.Error() }

// EKSException returns the AWS exception name and HTTP status for this error.
func (e *apiError) EKSException() (exception string, status int) { return e.exception, e.status }

// Unwrap exposes the canonical error so errors.As(&*cerrors.Error) matches and
// cerrors.GetCode reads the right code.
func (e *apiError) Unwrap() error { return e.err }

// resourceInUseErrf builds a ResourceInUseException (HTTP 409) carrying a
// FailedPrecondition canonical code.
func resourceInUseErrf(format string, args ...any) error {
	return &apiError{
		err:       cerrors.Newf(cerrors.FailedPrecondition, format, args...),
		exception: "ResourceInUseException",
		status:    http.StatusConflict,
	}
}
