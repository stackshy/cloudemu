package kendra

import (
	"regexp"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// conflict builds a ConflictException-tagged error for an operation on a
// resource that is not in a state that allows it.
func conflict(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConflict,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// conflictErr builds an error tagged with the named exception (a ConflictException
// variant such as FeaturedResultsConflictException).
func conflictErr(exception, format string, args ...any) error {
	return &driver.APIError{
		Exception: exception,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// resourceInUse builds a ResourceInUseException-tagged error.
func resourceInUse(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceInUse,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// validation builds a ValidationException-tagged error for invalid input.
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// maxRoleArnLength is the documented maximum length of a RoleArn.
const maxRoleArnLength = 1284

// roleArnPattern is the RoleArn pattern the Kendra API documents for CreateIndex,
// UpdateIndex, CreateDataSource and UpdateDataSource. The documented trailing
// `.{0,1023}` exceeds RE2's repeat limit, so the resource part is matched as
// any non-newline run and the documented overall length cap is checked apart.
var roleArnPattern = regexp.MustCompile(`^arn:[a-z0-9-.]{1,63}:[a-z0-9-.]{0,63}:[a-z0-9-.]{0,63}:[a-z0-9-.]{0,63}:[^/][^\n]*$`)

// validateRoleArn rejects a RoleArn that does not match the documented pattern
// and length with a ValidationException.
func validateRoleArn(arn string) error {
	if len(arn) > maxRoleArnLength || !roleArnPattern.MatchString(arn) {
		return validation("RoleArn %q is not a valid ARN", arn)
	}

	return nil
}
