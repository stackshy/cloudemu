package cloudformation

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// ExceptionError pairs a canonical error with the CloudFormation exception
// name the wire layer reports for it. It unwraps to the canonical error, so
// the cerrors predicates still classify it.
type ExceptionError struct {
	err       *cerrors.Error
	exception string
}

// NewException wraps err so it is reported as the named exception.
func NewException(exception string, err *cerrors.Error) *ExceptionError {
	return &ExceptionError{err: err, exception: exception}
}

// Error implements the error interface.
func (e *ExceptionError) Error() string { return e.err.Error() }

// Exception returns the CloudFormation exception name.
func (e *ExceptionError) Exception() string { return e.exception }

// Unwrap exposes the canonical error.
func (e *ExceptionError) Unwrap() error { return e.err }
