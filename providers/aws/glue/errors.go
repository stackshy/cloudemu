package glue

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// entityNotFound builds an EntityNotFoundException-tagged error.
func entityNotFound(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExEntityNotFound, Err: errors.Newf(errors.NotFound, format, args...)}
}

// schemaVersionNotFound builds a SchemaVersionNotFoundException-tagged error,
// used when a schema exists but the requested version does not.
func schemaVersionNotFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExSchemaVersionParseNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// alreadyExists builds an AlreadyExistsException-tagged error.
func alreadyExists(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExAlreadyExists, Err: errors.Newf(errors.AlreadyExists, format, args...)}
}

// invalidInput builds an InvalidInputException-tagged error.
func invalidInput(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidInput, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// concurrentModification builds a ConcurrentModificationException-tagged error.
func concurrentModification(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConcurrentModification,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// resourceNumberLimit builds a ResourceNumberLimitExceededException-tagged error.
func resourceNumberLimit(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNumberLimit,
		Err:       errors.Newf(errors.ResourceExhausted, format, args...),
	}
}

// crawlerNotRunning builds a CrawlerNotRunningException-tagged error.
func crawlerNotRunning(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExCrawlerNotRunning,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// conditionCheckFailure builds a ConditionCheckFailureException-tagged error,
// used when a conditional PutResourcePolicy precondition is not met.
func conditionCheckFailure(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExConditionCheckFailure,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}

// versionMismatch builds a VersionMismatchException-tagged error, used when a
// PutResourcePolicy PolicyHashCondition does not match the stored policy hash.
func versionMismatch(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExVersionMismatch,
		Err:       errors.Newf(errors.FailedPrecondition, format, args...),
	}
}
