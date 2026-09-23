package cloudwatch

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// The cores in this file hold the PutMetricAlarm and SetAlarmState logic.
// The query and the CBOR codecs both call them.

// validComparisonOperators is the closed CloudWatch ComparisonOperator enum.
// AWS rejects any other value with a ValidationError. Storing it would leave
// the alarm unable to fire.
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validComparisonOperators = map[string]bool{
	"GreaterThanOrEqualToThreshold":            true,
	"GreaterThanThreshold":                     true,
	"LessThanThreshold":                        true,
	"LessThanOrEqualToThreshold":               true,
	"LessThanLowerOrGreaterThanUpperThreshold": true,
	"LessThanLowerThreshold":                   true,
	"GreaterThanUpperThreshold":                true,
}

// comparisonOperatorValid reports whether op is empty or in the enum. Metric
// math and anomaly alarms may leave it out.
func comparisonOperatorValid(op string) bool {
	return op == "" || validComparisonOperators[op]
}

// putMetricAlarmCore validates the alarm and then stores it. A rejected
// request never reaches the driver.
func (h *Handler) putMetricAlarmCore(ctx context.Context, cfg *mondriver.AlarmConfig) error {
	if !comparisonOperatorValid(cfg.ComparisonOperator) {
		return newWireError(errValidation, "Invalid ComparisonOperator: "+cfg.ComparisonOperator)
	}

	if cfg.Unit != "" && !alarmeval.ValidUnit(cfg.Unit) {
		return newWireError(errValidation, "1 validation error detected: Value '"+cfg.Unit+
			"' at 'unit' failed to satisfy constraint: Member must satisfy enum value set: ["+
			strings.Join(alarmeval.Units(), ", ")+"]")
	}

	return h.monitoring.CreateAlarm(ctx, *cfg)
}

// errInvalidFormat is the code for StateReasonData that is not JSON.
const errInvalidFormat = "InvalidFormat"

// SetAlarmState length limits from the API reference.
const (
	maxStateReasonLen     = 1023
	maxStateReasonDataLen = 4000
)

// setAlarmStateInput is the shared SetAlarmState request. Pointers tell an
// absent field from an empty one, since StateReason may be empty but not absent.
type setAlarmStateInput struct {
	AlarmName       *string `cbor:"AlarmName"`
	StateValue      *string `cbor:"StateValue"`
	StateReason     *string `cbor:"StateReason"`
	StateReasonData *string `cbor:"StateReasonData"`
}

// alarmStateReasonDataSetter is the AWS-local capability that stores
// StateReasonData with the new state.
type alarmStateReasonDataSetter interface {
	SetAlarmStateWithData(ctx context.Context, name, state, reason, reasonData string) error
}

// setAlarmStateCore validates the request and then sets the state. A rejected
// request never reaches the driver, so the stored state stays as it was.
func (h *Handler) setAlarmStateCore(ctx context.Context, in *setAlarmStateInput) error {
	if err := validateSetAlarmState(in); err != nil {
		return err
	}

	if setter, ok := h.monitoring.(alarmStateReasonDataSetter); ok {
		var data string
		if in.StateReasonData != nil {
			data = *in.StateReasonData
		}

		return setter.SetAlarmStateWithData(ctx, *in.AlarmName, *in.StateValue, *in.StateReason, data)
	}

	return h.monitoring.SetAlarmState(ctx, *in.AlarmName, *in.StateValue, *in.StateReason)
}

func validateSetAlarmState(in *setAlarmStateInput) error {
	for _, f := range []struct {
		name string
		v    *string
	}{{"alarmName", in.AlarmName}, {"stateValue", in.StateValue}, {"stateReason", in.StateReason}} {
		if f.v == nil {
			return newWireError(errValidation, "1 validation error detected: Value null at '"+f.name+
				"' failed to satisfy constraint: Member must not be null")
		}
	}

	if !alarmeval.ValidState(*in.StateValue) {
		return newWireError(errValidation, "1 validation error detected: Value '"+*in.StateValue+
			"' at 'stateValue' failed to satisfy constraint: Member must satisfy enum value set: [INSUFFICIENT_DATA, ALARM, OK]")
	}

	if utf8.RuneCountInString(*in.StateReason) > maxStateReasonLen {
		return newWireError(errValidation, "1 validation error detected: Value at 'stateReason' failed to satisfy constraint: "+
			"Member must have length less than or equal to 1023")
	}

	if in.StateReasonData == nil || *in.StateReasonData == "" {
		return nil
	}

	if utf8.RuneCountInString(*in.StateReasonData) > maxStateReasonDataLen {
		return newWireError(errValidation, "1 validation error detected: Value at 'stateReasonData' failed to satisfy constraint: "+
			"Member must have length less than or equal to 4000")
	}

	if !json.Valid([]byte(*in.StateReasonData)) {
		return newWireError(errInvalidFormat, "Data was not syntactically valid JSON")
	}

	return nil
}
