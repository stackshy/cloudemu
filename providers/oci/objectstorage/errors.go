package objectstorage

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Object Storage error codes the portable error codes cannot name.
const (
	CodeBucketAlreadyExists = "BucketAlreadyExists"
	CodeBucketNotEmpty      = "BucketNotEmpty"
	CodeIfMatchFailed       = "IfMatchFailed"
	CodeIfNoneMatchFailed   = "IfNoneMatchFailed"
)

// ServiceError tags a portable error with the code OCI Object Storage reports
// for it. It unwraps to the portable error, so cerrors.GetCode still applies.
type ServiceError struct {
	Code string
	Err  *cerrors.Error
}

func (e *ServiceError) Error() string { return e.Err.Error() }

func (e *ServiceError) Unwrap() error { return e.Err }

func serviceErrorf(code string, portable cerrors.Code, format string, args ...any) error {
	return &ServiceError{Code: code, Err: cerrors.Newf(portable, format, args...)}
}

// checkETag applies if-match and if-none-match to a write against the current
// ETag, empty when nothing exists yet. if-none-match accepts only "*", as OCI's
// writes do.
func checkETag(current, ifMatch, ifNoneMatch string) error {
	if ifMatch != "" && (current == "" || (ifMatch != "*" && ifMatch != current)) {
		return serviceErrorf(CodeIfMatchFailed, cerrors.FailedPrecondition,
			"the if-match ETag %q does not match the current ETag", ifMatch)
	}

	switch ifNoneMatch {
	case "":
	case "*":
		if current != "" {
			return serviceErrorf(CodeIfNoneMatchFailed, cerrors.FailedPrecondition,
				"if-none-match is * but the resource already exists")
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "if-none-match supports only *, got %q", ifNoneMatch)
	}

	return nil
}
