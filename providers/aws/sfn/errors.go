package sfn

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

func smNotFound(arn string) error {
	return &driver.APIError{
		Exception: driver.ExStateMachineDoesNotExist,
		Err:       errors.Newf(errors.NotFound, "state machine %q does not exist", arn),
	}
}

func invalidDefinition(msg string) error {
	return &driver.APIError{
		Exception: driver.ExInvalidDefinition,
		Err:       errors.New(errors.InvalidArgument, msg),
	}
}

func smAlreadyExists(name string) error {
	return &driver.APIError{
		Exception: driver.ExStateMachineAlreadyExists,
		Err:       errors.Newf(errors.AlreadyExists, "state machine %q already exists", name),
	}
}

func execNotFound(arn string) error {
	return &driver.APIError{
		Exception: driver.ExExecutionDoesNotExist,
		Err:       errors.Newf(errors.NotFound, "execution %q does not exist", arn),
	}
}

func execAlreadyExists(name string) error {
	return &driver.APIError{
		Exception: driver.ExExecutionAlreadyExists,
		Err:       errors.Newf(errors.AlreadyExists, "execution %q already exists", name),
	}
}

func activityNotFound(arn string) error {
	return &driver.APIError{
		Exception: driver.ExActivityDoesNotExist,
		Err:       errors.Newf(errors.NotFound, "activity %q does not exist", arn),
	}
}

func activityAlreadyExists(name string) error {
	return &driver.APIError{
		Exception: driver.ExActivityAlreadyExists,
		Err:       errors.Newf(errors.AlreadyExists, "activity %q already exists", name),
	}
}

func mapRunNotFound(arn string) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, "map run %q does not exist", arn),
	}
}

func resourceNotFound(arn string) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, "resource %q not found", arn),
	}
}

// invalidArn builds an InvalidArn-tagged error.
func invalidArn(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidArn, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// invalidName builds an InvalidName-tagged error.
func invalidName(msg string) error {
	return &driver.APIError{Exception: driver.ExInvalidName, Err: errors.New(errors.InvalidArgument, msg)}
}

// invalidExecutionInput builds an InvalidExecutionInput-tagged error, returned
// when a StartExecution Input is not valid JSON.
func invalidExecutionInput(msg string) error {
	return &driver.APIError{Exception: driver.ExInvalidExecutionInput, Err: errors.New(errors.InvalidArgument, msg)}
}

// invalidToken builds an InvalidToken-tagged error.
func invalidToken(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidToken, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// tooManyTags builds a TooManyTags-tagged error.
func tooManyTags(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExTooManyTags, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// conflict builds a ConflictException-tagged error.
func conflict(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExConflict, Err: errors.Newf(errors.AlreadyExists, format, args...)}
}

// validationErr builds a ValidationException-tagged error.
func validationErr(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExValidation, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}
