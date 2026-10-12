package driver

import (
	"errors"
	"fmt"
)

// SageMaker exception names, as they appear in the X-Amzn-ErrorType header and
// __type member of an awsJson1_1 error. The sagemaker SDK models only
// ConflictException, ResourceInUse, ResourceLimitExceeded and ResourceNotFound;
// everything else (ValidationException included) surfaces as a generic API
// error carrying the same code.
const (
	ExValidation            = "ValidationException"
	ExResourceNotFound      = "ResourceNotFound"
	ExResourceInUse         = "ResourceInUse"
	ExResourceLimitExceeded = "ResourceLimitExceeded"
	ExConflict              = "ConflictException"
)

// APIError tags an error with the SageMaker exception it must surface as on the
// wire, so the handler renders the exact name the SDK matches on rather than a
// generic code mapping.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }

// Conflict returns a ConflictException (an update or delete that conflicts
// with the resource's current state or with another in-flight operation).
func Conflict(format string, args ...any) error {
	return &APIError{Exception: ExConflict, Err: fmt.Errorf(format, args...)} //nolint:err113 // typed API error carrying a formatted message
}

// InUse returns a ResourceInUse error (deleting or changing a resource that
// other resources still depend on).
func InUse(format string, args ...any) error {
	return &APIError{Exception: ExResourceInUse, Err: fmt.Errorf(format, args...)} //nolint:err113,lll // typed API error carrying a formatted message; SDK-shaped literal
}

// LimitExceeded returns a ResourceLimitExceeded error (an account quota).
func LimitExceeded(format string, args ...any) error {
	return &APIError{Exception: ExResourceLimitExceeded, Err: fmt.Errorf(format, args...)} //nolint:err113,lll // typed API error carrying a formatted message; SDK-shaped literal
}

// ExceptionOf returns the exception tagged on err and true, or "" and false.
func ExceptionOf(err error) (string, bool) {
	var api *APIError
	if errors.As(err, &api) {
		return api.Exception, true
	}

	return "", false
}

// ExModelError is the runtime exception for a model container that answered an
// invocation with a non-2xx status (HTTP 424 on the wire).
const ExModelError = "ModelError"

// ModelError is returned by InvokeEndpoint when the endpoint's model container
// failed the call. OriginalStatusCode is the status the container returned
// (reported to CloudWatch as a 4XX or a 5XX invocation error).
type ModelError struct {
	OriginalStatusCode int
	Message            string
}

func (e *ModelError) Error() string { return e.Message }
