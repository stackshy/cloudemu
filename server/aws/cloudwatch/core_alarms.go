package cloudwatch

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
)

// The cores in this file hold the SetAlarmState logic. The query and the
// CBOR codecs both call them.

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

// setAlarmStateCore validates the request and then sets the state. A rejected
// request never reaches the driver, so the stored state stays as it was.
func (h *Handler) setAlarmStateCore(ctx context.Context, in *setAlarmStateInput) error {
	if err := validateSetAlarmState(in); err != nil {
		return err
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
