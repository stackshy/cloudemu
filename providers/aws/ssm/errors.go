package ssm

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/errors"
)

// SSM exception names this package raises directly.
const (
	excDocumentAlreadyExists        = "DocumentAlreadyExists"
	excDocumentLimitExceeded        = "DocumentLimitExceeded"
	excDocumentVersionLimitExceeded = "DocumentVersionLimitExceeded"
	excDocumentPermissionLimit      = "DocumentPermissionLimit"
	excDuplicateDocumentContent     = "DuplicateDocumentContent"
	excDuplicateDocumentVersionName = "DuplicateDocumentVersionName"
	excInvalidDocument              = "InvalidDocument"
	excInvalidDocumentContent       = "InvalidDocumentContent"
	excInvalidDocumentOperation     = "InvalidDocumentOperation"
	excInvalidDocumentSchemaVersion = "InvalidDocumentSchemaVersion"
	excInvalidDocumentVersion       = "InvalidDocumentVersion"
	excInvalidFilterKey             = "InvalidFilterKey"
	excInvalidInstanceID            = "InvalidInstanceId"
	excInvalidPermissionType        = "InvalidPermissionType"
	excMaxDocumentSizeExceeded      = "MaxDocumentSizeExceeded"
	excValidation                   = "ValidationException"
)

// apiError pairs a canonical cloudemu error with the SSM exception name the
// wire layer must surface. The generic code mapping in the server turns every
// NotFound into ParameterNotFound, which is wrong outside Parameter Store. It
// unwraps to the *errors.Error so the errors.IsX predicates still classify it.
type apiError struct {
	err       *errors.Error
	exception string
}

// Error implements the error interface.
func (e *apiError) Error() string { return e.err.Error() }

// Unwrap exposes the canonical error.
func (e *apiError) Unwrap() error { return e.err }

// SSMException returns the exception name and HTTP status. Every SSM client
// error is HTTP 400.
func (e *apiError) SSMException() (exception string, status int) {
	return e.exception, http.StatusBadRequest
}

// ssmErrf builds an apiError with the given exception and canonical code.
func ssmErrf(exception string, code errors.Code, format string, args ...any) error {
	return &apiError{err: errors.Newf(code, format, args...), exception: exception}
}

// invalidDocumentErr is the InvalidDocument error for a name that does not
// resolve. Terraform matches on the exception name, and older provider
// versions also on this message.
func invalidDocumentErr(name string) error {
	return ssmErrf(excInvalidDocument, errors.NotFound, "Document with name %s does not exist.", name)
}

// awsOwnedErr rejects a change to a read-only AWS-owned document.
func awsOwnedErr(name string) error {
	return ssmErrf(excInvalidDocumentOperation, errors.FailedPrecondition,
		"Document %s is owned by Amazon and can't be modified or deleted.", name)
}
