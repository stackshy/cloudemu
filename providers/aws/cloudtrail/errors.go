package cloudtrail

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// apiErr builds a driver.APIError tagging a canonical error with a CloudTrail
// exception name so the wire layer can emit the right X-Amzn-Errortype.
func apiErr(exception string, code errors.Code, format string, args ...any) error {
	return &driver.APIError{Exception: exception, Err: errors.Newf(code, format, args...)}
}

func errTrailNotFound(nameOrARN string) error {
	return apiErr(driver.ExTrailNotFound, errors.NotFound, "trail %q not found", nameOrARN)
}

func errTrailExists(name string) error {
	return apiErr(driver.ExTrailAlreadyExists, errors.AlreadyExists, "trail %q already exists", name)
}

func errInvalidTrailName(format string, args ...any) error {
	return apiErr(driver.ExInvalidTrailName, errors.InvalidArgument, format, args...)
}

func errEDSNotFound(arn string) error {
	return apiErr(driver.ExEventDataStoreNotFound, errors.NotFound, "event data store %q not found", arn)
}

func errEDSExists(name string) error {
	return apiErr(driver.ExEventDataStoreAlreadyEx, errors.AlreadyExists, "event data store %q already exists", name)
}

func errEDSARNInvalid(arn string) error {
	return apiErr(driver.ExEventDataStoreARNInvalid, errors.InvalidArgument,
		"%q is not a valid event data store ARN", arn)
}

func errEDSInvalidStatus(format string, args ...any) error {
	return apiErr(driver.ExInvalidEDSStatus, errors.FailedPrecondition, format, args...)
}

func errInvalidParameter(format string, args ...any) error {
	return apiErr(driver.ExInvalidParameter, errors.InvalidArgument, format, args...)
}

func errChannelNotFound(arn string) error {
	return apiErr(driver.ExChannelNotFound, errors.NotFound, "channel %q not found", arn)
}

func errChannelExists(name string) error {
	return apiErr(driver.ExChannelAlreadyExists, errors.AlreadyExists, "channel %q already exists", name)
}

func errChannelARNInvalid(arn string) error {
	return apiErr(driver.ExChannelARNInvalid, errors.InvalidArgument, "%q is not a valid channel ARN", arn)
}

func errResourceNotFound(id string) error {
	return apiErr(driver.ExResourceNotFound, errors.NotFound, "resource %q not found", id)
}

func errCloudTrailARNInvalid(arn string) error {
	return apiErr(driver.ExCloudTrailARNInvalid, errors.InvalidArgument,
		"%q is not a valid CloudTrail resource ARN", arn)
}

func errImportNotFound(id string) error {
	return apiErr(driver.ExImportNotFound, errors.NotFound, "import %q not found", id)
}

func errQueryNotFound(id string) error {
	return apiErr(driver.ExQueryIDNotFound, errors.NotFound, "query %q not found", id)
}

func errResourcePolicyNotFound(arn string) error {
	return apiErr(driver.ExResourcePolicyNotFound, errors.NotFound, "no resource policy for %q", arn)
}

func errEDSTerminationProtected(arn string) error {
	return apiErr(driver.ExEDSTerminationProtected, errors.FailedPrecondition,
		"event data store %q has termination protection enabled", arn)
}
