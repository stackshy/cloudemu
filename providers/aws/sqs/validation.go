package sqs

import (
	stderrors "errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// SQS request limits, per the SQS API reference.
const (
	fifoSuffix            = ".fifo"
	maxMessageDelay       = 900
	maxReceiveVisibility  = 43200
	minReceiveMessages    = 1
	batchCodeInvalidParam = "InvalidParameterValue"
	batchCodeInternal     = "InternalError"
	attrTypeBinary        = "Binary"
)

// A queue name is 1 to 80 characters of [A-Za-z0-9_-]. A FIFO name has the
// same charset and must end in .fifo, and the suffix counts toward the 80.
var (
	standardQueueName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	fifoQueueName     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,75}\.fifo$`)
)

// validateQueueName applies the CreateQueue name rules. A standard queue
// can't contain a dot, so a ".fifo" name without FifoQueue=true fails here
// just as it does on real SQS.
func validateQueueName(name string, fifo bool) error {
	if name == "" {
		return errors.New(errors.InvalidArgument, "queue name is required")
	}

	if fifo {
		if !strings.HasSuffix(name, fifoSuffix) {
			return errors.New(errors.InvalidArgument, "FIFO queue name must end with .fifo")
		}

		if !fifoQueueName.MatchString(name) {
			return errors.New(errors.InvalidArgument,
				"The name of a FIFO queue can only include alphanumeric characters, hyphens, or underscores, "+
					"must end with .fifo suffix and be 1 to 80 in length.")
		}

		return nil
	}

	if !standardQueueName.MatchString(name) {
		return errors.New(errors.InvalidArgument,
			"Can only include alphanumeric characters, hyphens, or underscores. 1 to 80 in length")
	}

	return nil
}

// validateMessageBody checks that the body is present and uses only the
// characters SQS accepts. Invalid UTF-8 is rejected too, because ranging
// over it yields U+FFFD, which is itself an allowed character.
func validateMessageBody(body string) error {
	if body == "" {
		return driver.ErrMissingParameter
	}

	if !utf8.ValidString(body) {
		return driver.ErrInvalidMessageContents
	}

	for _, r := range body {
		if !allowedBodyRune(r) {
			return driver.ErrInvalidMessageContents
		}
	}

	return nil
}

func allowedBodyRune(r rune) bool {
	switch {
	case r == '\t', r == '\n', r == '\r':
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	default:
		return r >= 0x10000 && r <= 0x10FFFF
	}
}

// validateMessageDelay checks the per-message DelaySeconds. FIFO queues only
// support a queue-level delay, so any positive per-message value is refused.
func validateMessageDelay(fifo bool, delay int) error {
	if delay < 0 || delay > maxMessageDelay {
		return errors.Newf(errors.InvalidArgument,
			"Value %d for parameter DelaySeconds is invalid. Reason: DelaySeconds must be >= 0 and <= 900.", delay)
	}

	if fifo && delay > 0 {
		return errors.Newf(errors.InvalidArgument,
			"Value %d for parameter DelaySeconds is invalid. "+
				"Reason: The request include parameter that is not valid for this queue type.", delay)
	}

	return nil
}

// validMessageAttributeType reports whether base is a DataType prefix SQS
// accepts. A custom label may follow after a dot, as in "String.json".
func validMessageAttributeType(base string) bool {
	return base == "String" || base == "Number" || base == attrTypeBinary
}

// validateMessageAttributes checks each typed attribute's DataType and that it
// carries a non-empty value in the field that type uses.
func validateMessageAttributes(attrs map[string]driver.MessageAttributeValue) error {
	for name, v := range attrs {
		base, _, _ := strings.Cut(v.DataType, ".")
		if !validMessageAttributeType(base) {
			return errors.Newf(errors.InvalidArgument,
				"The type of message (user) attribute '%s' is invalid. "+
					"You must use only the following supported type prefixes: Binary, Number, String.", name)
		}

		empty := v.StringValue == ""
		if base == attrTypeBinary {
			empty = len(v.BinaryValue) == 0
		}

		if empty {
			return errors.Newf(errors.InvalidArgument,
				"The message (user) attribute '%s' must contain a non-empty value of message attribute type '%s'.",
				name, v.DataType)
		}
	}

	return nil
}

// validateSendInput runs the request checks that don't depend on queue state
// other than its type.
func validateSendInput(fifo bool, input *driver.SendMessageInput) error {
	if err := validateMessageBody(input.Body); err != nil {
		return err
	}

	if err := validateMessageDelay(fifo, input.DelaySeconds); err != nil {
		return err
	}

	return validateMessageAttributes(input.MessageAttributes)
}

// validateReceive rejects out-of-range receive parameters. SQS refuses them
// with InvalidParameterValue rather than clamping. maxSet reports that
// MaxNumberOfMessages was supplied, because only an explicit 0 is an error.
func validateReceive(maxMessages int, maxSet bool, waitSeconds, visibility int) error {
	if maxMessages > maxReceiveMessages || maxMessages < 0 || (maxSet && maxMessages < minReceiveMessages) {
		return errors.Newf(errors.InvalidArgument,
			"Value %d for parameter MaxNumberOfMessages is invalid. Reason: Must be between 1 and 10, if provided.",
			maxMessages)
	}

	if waitSeconds < 0 || waitSeconds > maxReceiveWaitSeconds {
		return errors.Newf(errors.InvalidArgument,
			"Value %d for parameter WaitTimeSeconds is invalid. Reason: Must be >= 0 and <= 20, if provided.",
			waitSeconds)
	}

	if visibility < 0 || visibility > maxReceiveVisibility {
		return errors.Newf(errors.InvalidArgument,
			"Value %d for parameter VisibilityTimeout is invalid. Reason: Must be >= 0 and <= 43200, if provided.",
			visibility)
	}

	return nil
}

// batchFailureCode picks the SQS error code for a failed SendMessageBatch
// entry, so a batch reports the same code a single SendMessage would.
func batchFailureCode(err error) string {
	switch {
	case stderrors.Is(err, driver.ErrMissingParameter):
		return "MissingParameter"
	case stderrors.Is(err, driver.ErrInvalidMessageContents):
		return "InvalidMessageContents"
	case errors.IsInvalidArgument(err):
		return batchCodeInvalidParam
	default:
		return batchCodeInternal
	}
}
