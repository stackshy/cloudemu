package eventbridge

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// scheduleExpressionInvalidMessage is the ValidationException detail real
// EventBridge returns when PutRule is given a ScheduleExpression that is neither
// a well-formed rate(...) nor cron(...) expression.
const scheduleExpressionInvalidMessage = "Parameter ScheduleExpression is not valid."

const (
	rateExpressionFieldCount = 2 // rate(value unit)
	cronExpressionFieldCount = 6 // cron(minutes hours day-of-month month day-of-week year)
)

// validateRuleInput validates the caller-supplied fields of a PutRule request
// that EventBridge rejects up front: a missing name, a State outside the
// documented enum, and a malformed ScheduleExpression. Bundling them keeps
// PutRule's own control flow within the complexity budget.
func validateRuleInput(cfg *driver.RuleConfig) error {
	if cfg.Name == "" {
		return errors.New(errors.InvalidArgument, "rule name is required")
	}

	if err := validateRuleState(cfg.State); err != nil {
		return err
	}

	if cfg.ScheduleExpression != "" {
		return validateScheduleExpression(cfg.ScheduleExpression)
	}

	return nil
}

// validateScheduleExpression validates an EventBridge ScheduleExpression. Real
// EventBridge accepts only the rate(...) and cron(...) forms and rejects
// anything else with a ValidationException. Without this check a typo'd
// expression (e.g. "every 5 minutes", a bare "rate(5 hour)", or a 5-field Unix
// cron) would store "successfully" and then never self-trigger — a silent
// misconfiguration the caller never sees.
func validateScheduleExpression(expr string) error {
	switch {
	case strings.HasPrefix(expr, "rate(") && strings.HasSuffix(expr, ")"):
		return validateRateExpression(expr[len("rate(") : len(expr)-1])
	case strings.HasPrefix(expr, "cron(") && strings.HasSuffix(expr, ")"):
		return validateCronExpression(expr[len("cron(") : len(expr)-1])
	default:
		return errScheduleInvalid()
	}
}

// validateRateExpression validates the body of a rate(...) expression:
// "value unit", where value is a positive integer and unit is one of
// minute(s)/hour(s)/day(s), with singular/plural agreement (value 1 is
// singular, value >1 is plural) — the exact rule real EventBridge enforces.
func validateRateExpression(body string) error {
	fields := strings.Fields(body)
	if len(fields) != rateExpressionFieldCount {
		return errScheduleInvalid()
	}

	value, err := strconv.Atoi(fields[0])
	if err != nil || value < 1 {
		return errScheduleInvalid()
	}

	if !rateUnitMatchesValue(fields[1], value) {
		return errScheduleInvalid()
	}

	return nil
}

// rateUnitMatchesValue reports whether a rate-expression unit is valid for the
// given value: the singular units require a value of 1, the plural units a
// value greater than 1.
func rateUnitMatchesValue(unit string, value int) bool {
	switch unit {
	case "minute", "hour", "day":
		return value == 1
	case "minutes", "hours", "days":
		return value > 1
	default:
		return false
	}
}

// validateCronExpression validates the body of a cron(...) expression, which
// EventBridge requires to have exactly six whitespace-separated fields
// (minutes, hours, day-of-month, month, day-of-week, year) — a standard
// five-field Unix cron is rejected. Per-field value validation is not modeled.
func validateCronExpression(body string) error {
	if len(strings.Fields(body)) != cronExpressionFieldCount {
		return errScheduleInvalid()
	}

	return nil
}

func errScheduleInvalid() error {
	return errors.New(errors.InvalidArgument, scheduleExpressionInvalidMessage)
}

// validateRuleState reports whether a PutRule State value is one EventBridge
// accepts. An empty state defaults to ENABLED downstream; any other value
// outside the documented enum is rejected with a ValidationException rather than
// silently stored.
func validateRuleState(state string) error {
	switch state {
	case "", ruleStateEnabled, ruleStateDisabled, ruleStateEnabledWithCloudTrail:
		return nil
	default:
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'state' failed to satisfy constraint: "+
				"Member must satisfy enum value set: "+
				"[ENABLED, DISABLED, ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS]", state)
	}
}

// ruleStateActive reports whether a rule in the given state matches events.
// Both ENABLED and ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS are active;
// DISABLED is not.
func ruleStateActive(state string) bool {
	return state == ruleStateEnabled || state == ruleStateEnabledWithCloudTrail
}
