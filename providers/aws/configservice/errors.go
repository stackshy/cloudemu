package configservice

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// Local aliases for the canonical cloudemu error codes used across this package.
const (
	invalidArgCode         = errors.InvalidArgument
	failedPreconditionCode = errors.FailedPrecondition
	notFoundCode           = errors.NotFound
)

// tagged builds a driver.APIError carrying a specific Config exception name over
// a canonical cloudemu error (so HTTP status resolves via Unwrap).
func tagged(exception string, code errors.Code, format string, args ...any) error {
	return &driver.APIError{Exception: exception, Err: errors.Newf(code, format, args...)}
}

func noSuchConfigRule(name string) error {
	return tagged(driver.ExNoSuchConfigRule, errors.NotFound, "the Config rule %q does not exist", name)
}

func noSuchRecorder(name string) error {
	return tagged(driver.ExNoSuchConfigurationRecorder, errors.NotFound,
		"the configuration recorder %q does not exist", name)
}

func noSuchDeliveryChannel(name string) error {
	return tagged(driver.ExNoSuchDeliveryChannel, errors.NotFound, "the delivery channel %q does not exist", name)
}

func noSuchAggregator(name string) error {
	return tagged(driver.ExNoSuchConfigurationAggregator, errors.NotFound,
		"the configuration aggregator %q does not exist", name)
}

func noSuchConformancePack(name string) error {
	return tagged(driver.ExNoSuchConformancePack, errors.NotFound, "the conformance pack %q does not exist", name)
}

func noSuchOrgConfigRule(name string) error {
	return tagged(driver.ExNoSuchOrganizationConfigRule, errors.NotFound,
		"the organization config rule %q does not exist", name)
}

func noSuchOrgConformancePack(name string) error {
	return tagged(driver.ExNoSuchOrganizationConformancePack, errors.NotFound,
		"the organization conformance pack %q does not exist", name)
}

func noSuchRemediationConfig(ruleName string) error {
	return tagged(driver.ExNoSuchRemediationConfiguration, errors.NotFound,
		"no remediation configuration for rule %q", ruleName)
}

func noSuchRetentionConfig(name string) error {
	return tagged(driver.ExNoSuchRetentionConfiguration, errors.NotFound,
		"the retention configuration %q does not exist", name)
}

func noSuchStoredQuery(name string) error {
	return tagged(driver.ExResourceNotFound, errors.NotFound, "the stored query %q does not exist", name)
}

func invalidParameter(format string, args ...any) error {
	return tagged(driver.ExInvalidParameterValue, errors.InvalidArgument, format, args...)
}

func validation(format string, args ...any) error {
	return tagged(driver.ExValidation, errors.InvalidArgument, format, args...)
}

func invalidNextToken(token string) error {
	return tagged(driver.ExInvalidNextToken, errors.InvalidArgument, "the nextToken %q is invalid", token)
}

func maxRecordersExceeded() error {
	return tagged(driver.ExMaxNumberOfConfigurationRecordersExceeded, errors.FailedPrecondition,
		"an account supports only one configuration recorder")
}

func maxDeliveryChannelsExceeded() error {
	return tagged(driver.ExMaxNumberOfDeliveryChannelsExceeded, errors.FailedPrecondition,
		"an account supports only one delivery channel")
}

func tooManyTags(limit int) error {
	return tagged(driver.ExTooManyTags, errors.InvalidArgument, "a resource may have at most %d tags", limit)
}

func resourceInUse(format string, args ...any) error {
	return tagged(driver.ExResourceInUse, errors.FailedPrecondition, format, args...)
}

func noAvailableDeliveryChannel() error {
	return tagged(driver.ExNoAvailableDeliveryChannel, errors.FailedPrecondition,
		"recording cannot start without a delivery channel")
}
