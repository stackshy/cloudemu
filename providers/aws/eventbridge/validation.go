package eventbridge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// resourceNamePattern is the character set EventBridge allows in archive,
// replay, connection and API destination names, as its error messages quote it.
const resourceNamePattern = `[\.\-_A-Za-z0-9]+`

var resourceNameRE = regexp.MustCompile(`^[.\-_A-Za-z0-9]+$`)

// eventsARNPattern is the shape EventBridge requires of an event bus or archive
// ARN parameter.
const eventsARNPattern = `^arn:aws([a-z]|\-)*:events:([a-z]|\d|\-)*:([0-9]{12})?:.+\/.+$`

var eventsARNRE = regexp.MustCompile(eventsARNPattern)

// violations collects AWS input-constraint failures and renders them the way
// EventBridge reports them: "N validation error(s) detected: Value 'v' at
// 'field' failed to satisfy constraint: ...", joined with "; ".
type violations []string

func (v *violations) add(value, field, constraint string) {
	*v = append(*v, fmt.Sprintf("Value '%s' at '%s' failed to satisfy constraint: %s", value, field, constraint))
}

// name checks a resource name against its maximum length and the name pattern.
func (v *violations) name(value, field string, maxLen int) {
	if value == "" {
		v.add(value, field, "Member must have length greater than or equal to 1")

		return
	}

	if len(value) > maxLen {
		v.add(value, field, fmt.Sprintf("Member must have length less than or equal to %d", maxLen))
	}

	if !resourceNameRE.MatchString(value) {
		v.add(value, field, "Member must satisfy regular expression pattern: "+resourceNamePattern)
	}
}

func (v *violations) maxLen(value, field string, limit int) {
	if len(value) > limit {
		v.add(value, field, fmt.Sprintf("Member must have length less than or equal to %d", limit))
	}
}

func (v *violations) enum(value, field string, allowed []string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}

	v.add(value, field, "Member must satisfy enum value set: ["+strings.Join(allowed, ", ")+"]")
}

// addSensitive records a failure on a member the API model marks sensitive
// (passwords, API key values, client secrets, HTTP parameter values). The value
// is left out of the message, as AWS does, so a credential never reaches an
// error response or log.
func (v *violations) addSensitive(field, constraint string) {
	*v = append(*v, fmt.Sprintf("Value at '%s' failed to satisfy constraint: %s", field, constraint))
}

// required checks that value is set and at most maxLen bytes long.
func (v *violations) required(value, field string, maxLen int) {
	if c := lengthConstraint(value, maxLen); c != "" {
		v.add(value, field, c)
	}
}

// requiredSensitive is required for a sensitive member: the value is never
// echoed in the message.
func (v *violations) requiredSensitive(value, field string, maxLen int) {
	if c := lengthConstraint(value, maxLen); c != "" {
		v.addSensitive(field, c)
	}
}

// maxLenSensitive checks that a sensitive, optional value is at most maxLen
// bytes long without echoing it.
func (v *violations) maxLenSensitive(value, field string, maxLen int) {
	if len(value) > maxLen {
		v.addSensitive(field, fmt.Sprintf("Member must have length less than or equal to %d", maxLen))
	}
}

// lengthConstraint returns the constraint a required value of at most maxLen
// bytes breaks, or "" when it is within bounds.
func lengthConstraint(value string, maxLen int) string {
	switch {
	case value == "":
		return "Member must have length greater than or equal to 1"
	case len(value) > maxLen:
		return fmt.Sprintf("Member must have length less than or equal to %d", maxLen)
	default:
		return ""
	}
}

// pattern checks value against re, quoting patternText (the pattern as the AWS
// API model spells it) in the message.
func (v *violations) pattern(value, field string, re *regexp.Regexp, patternText string) {
	if !re.MatchString(value) {
		v.add(value, field, "Member must satisfy regular expression pattern: "+patternText)
	}
}

// present records a required member the request left out, reporting whether
// value is set.
func (v *violations) present(value, field string) bool {
	if value != "" {
		return true
	}

	*v = append(*v, fmt.Sprintf("Value null at '%s' failed to satisfy constraint: Member must not be null", field))

	return false
}

// lengthBetween checks that a set value is minLen to maxLen bytes long.
func (v *violations) lengthBetween(value, field string, minLen, maxLen int) {
	switch {
	case len(value) < minLen:
		v.add(value, field, fmt.Sprintf("Member must have length greater than or equal to %d", minLen))
	case len(value) > maxLen:
		v.add(value, field, fmt.Sprintf("Member must have length less than or equal to %d", maxLen))
	}
}

func (v *violations) eventsARN(value, field string) {
	if !eventsARNRE.MatchString(value) {
		v.add(value, field, "Member must satisfy regular expression pattern: "+eventsARNPattern)
	}
}

// err returns the combined ValidationException, or nil when nothing failed.
func (v violations) err() error {
	switch len(v) {
	case 0:
		return nil
	case 1:
		return errors.New(errors.InvalidArgument, "1 validation error detected: "+v[0])
	default:
		return errors.Newf(errors.InvalidArgument, "%d validation errors detected: %s", len(v), strings.Join(v, "; "))
	}
}

// busNameFromARN returns the bus name an event bus ARN refers to, or "" when the
// ARN does not name an event bus.
func busNameFromARN(arn string) string {
	const infix = ":event-bus/"

	idx := strings.LastIndex(arn, infix)
	if idx == -1 {
		return ""
	}

	return arn[idx+len(infix):]
}

// Values shared by more than one EventBridge enum.
const (
	stateCreating = "CREATING"
	stateUpdating = "UPDATING"
	httpGET       = "GET"
	httpPOST      = "POST"
	httpPUT       = "PUT"
)

// countSet reports how many of the given strings are non-empty.
func countSet(values ...string) int {
	n := 0

	for _, s := range values {
		if s != "" {
			n++
		}
	}

	return n
}
