package ecr

import "github.com/stackshy/cloudemu/v2/errors"

// ECR exception names surfaced on the wire. Several ECR operations return the
// canonical NotFound code for distinct real-AWS exceptions (a missing
// repository, a missing image, a missing scan, a missing repository policy), so
// the provider tags the error with the precise exception name the server should
// surface rather than letting the generic code-based mapping collapse them all
// to RepositoryNotFoundException.
const (
	excRepositoryNotFound       = "RepositoryNotFoundException"
	excImageNotFound            = "ImageNotFoundException"
	excScanNotFound             = "ScanNotFoundException"
	excRepositoryPolicyNotFound = "RepositoryPolicyNotFoundException"
	excLifecyclePolicyNotFound  = "LifecyclePolicyNotFoundException"
)

// apiError pairs a canonical cloudemu error with the precise ECR exception name
// the server should surface. It unwraps to the underlying *errors.Error so the
// errors.IsX predicates keep classifying it, while exposing ECRException() for
// the server's error mapper.
type apiError struct {
	err       *errors.Error
	exception string
}

// Error implements the error interface.
func (e *apiError) Error() string { return e.err.Error() }

// ECRException returns the AWS exception name for this error.
func (e *apiError) ECRException() string { return e.exception }

// Unwrap exposes the canonical error so errors.As(&*errors.Error) matches and
// errors.GetCode reads the right code.
func (e *apiError) Unwrap() error { return e.err }

// apiErrf builds a NotFound apiError with a formatted message. Every ECR
// exception that needs a precise name here (repository, image, scan, or
// repository-policy not found) is a NotFound-category error, so the canonical
// code is fixed; the exception argument carries the AWS distinction.
func apiErrf(exception, format string, args ...any) error {
	return &apiError{err: errors.Newf(errors.NotFound, format, args...), exception: exception}
}
