package cloudwatchlogs

import (
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// CloudWatch Logs name and PutLogEvents limits, per the API reference.
const (
	maxLogNameLength       = 512
	maxPutLogEventsCount   = 10000
	maxPutLogEventsBytes   = 1048576
	putLogEventOverhead    = 26
	reservedLogGroupPrefix = "aws/"
)

var logGroupNamePattern = regexp.MustCompile(`^[.\-_/#A-Za-z0-9]+$`)

// validateLogGroupName applies the CreateLogGroup name rules. Only a bare
// "aws/" prefix is reserved, so "/aws/lambda/fn" stays valid.
func validateLogGroupName(name string) error {
	if name == "" {
		return errors.New(errors.InvalidArgument, "log group name is required")
	}

	if len(name) > maxLogNameLength {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'logGroupName' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxLogNameLength)
	}

	if !logGroupNamePattern.MatchString(name) {
		return errors.New(errors.InvalidArgument,
			"1 validation error detected: Value at 'logGroupName' failed to satisfy constraint: "+
				`Member must satisfy regular expression pattern: [\.\-_/#A-Za-z0-9]+`)
	}

	if strings.HasPrefix(name, reservedLogGroupPrefix) {
		return errors.New(errors.InvalidArgument, "Log group names can't start with the string aws/")
	}

	return nil
}

// validateLogStreamName applies the CreateLogStream name rules: 1 to 512
// characters with no colon or asterisk.
func validateLogStreamName(name string) error {
	if name == "" {
		return errors.New(errors.InvalidArgument, "stream name is required")
	}

	if len(name) > maxLogNameLength {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'logStreamName' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxLogNameLength)
	}

	if strings.ContainsAny(name, ":*") {
		return errors.New(errors.InvalidArgument,
			"1 validation error detected: Value at 'logStreamName' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: [^:*]*")
	}

	return nil
}

// validatePutLogEventsSize applies the PutLogEvents count and byte limits.
func validatePutLogEventsSize(events []driver.LogEvent) error {
	if len(events) > maxPutLogEventsCount {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value at 'logEvents' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxPutLogEventsCount)
	}

	total := 0

	for i := range events {
		if events[i].Message == "" {
			return errors.Newf(errors.InvalidArgument,
				"1 validation error detected: Value '' at 'logEvents.%d.member.message' failed to satisfy constraint: "+
					"Member must have length greater than or equal to 1", i+1)
		}

		total += len(events[i].Message) + putLogEventOverhead
	}

	if total > maxPutLogEventsBytes {
		return errors.New(errors.InvalidArgument,
			"Upload too large: the batch of log events exceeds the maximum of 1,048,576 bytes")
	}

	return nil
}
