package cloudwatch

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
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

	if err := validateAlarmMetrics(cfg); err != nil {
		return err
	}

	return h.monitoring.CreateAlarm(ctx, *cfg)
}

// metricQueryIDPattern is the MetricDataQuery Id rule from the API reference.
var metricQueryIDPattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9_]*$`)

// validateAlarmMetrics checks the Metrics list of a metric-math alarm. An
// expression outside the supported syntax is stored as is, because AWS accepts
// it. It evaluates to no data.
func validateAlarmMetrics(cfg *mondriver.AlarmConfig) error {
	if len(cfg.Metrics) == 0 {
		return validateSingleMetric(cfg)
	}

	if hasSingleMetricFields(cfg) {
		return newWireError(errValidation, "Metrics cannot be used with MetricName, Namespace, Dimensions, Period, Unit, "+
			"Statistic or ExtendedStatistic.")
	}

	ids, err := metricQueryIDs(cfg.Metrics)
	if err != nil {
		return err
	}

	if len(metricmath.Watched(cfg.Metrics, cfg.ThresholdMetricID)) != 1 {
		return newWireError(errValidation, "Exactly one element of the metrics list should return data.")
	}

	if cfg.ThresholdMetricID != "" && !ids[cfg.ThresholdMetricID] {
		return newWireError(errValidation, "ThresholdMetricId "+cfg.ThresholdMetricID+" does not match any Id in the metrics list.")
	}

	if err := expressionRefsKnown(cfg.Metrics, ids); err != nil {
		return err
	}

	if id, ok := metricmath.Cycle(cfg.Metrics); ok {
		return newWireError(errValidation, "Error in expression '"+id+"': Circular dependency in the metrics list.")
	}

	return nil
}

// validateSingleMetric checks an alarm without Metrics. It must name a
// metric and cannot use ThresholdMetricId.
func validateSingleMetric(cfg *mondriver.AlarmConfig) error {
	if cfg.ThresholdMetricID != "" {
		return newWireError(errValidation, "ThresholdMetricId can only be used with Metrics.")
	}

	if cfg.MetricName == "" {
		return newWireError(errValidation, "For each PutMetricAlarm operation, you must specify either MetricName, "+
			"a Metrics array, or an EvaluationCriteria.")
	}

	return nil
}

// hasSingleMetricFields reports whether any field of a single-metric alarm
// is set. Metrics replaces all of them.
func hasSingleMetricFields(cfg *mondriver.AlarmConfig) bool {
	return cfg.Namespace != "" || cfg.MetricName != "" || len(cfg.Dimensions) > 0 || cfg.Period != 0 ||
		cfg.Unit != "" || cfg.Stat != "" || cfg.ExtendedStatistic != ""
}

// metricQueryIDs checks each entry's Id and shape and returns the set of Ids.
func metricQueryIDs(queries []mondriver.MetricDataQuery) (map[string]bool, error) {
	ids := make(map[string]bool, len(queries))

	for i := range queries {
		q := &queries[i]

		if !metricQueryIDPattern.MatchString(q.ID) {
			return nil, newWireError(errValidation, "Invalid metrics list: the id '"+q.ID+
				"' must start with a lowercase letter and contain only letters, numbers and underscores.")
		}

		if ids[q.ID] {
			return nil, newWireError(errValidation, "Invalid metrics list: the id '"+q.ID+"' is used more than once.")
		}

		ids[q.ID] = true

		if (q.MetricStat == nil) == (q.Expression == "") {
			return nil, newWireError(errValidation, "Invalid metrics list: the element '"+q.ID+
				"' must specify exactly one of MetricStat and Expression.")
		}
	}

	return ids, nil
}

// expressionRefsKnown checks that each expression only reads Ids in the list.
func expressionRefsKnown(queries []mondriver.MetricDataQuery, ids map[string]bool) error {
	for i := range queries {
		refs, ok := metricmath.References(queries[i].Expression)
		if !ok {
			continue
		}

		for _, ref := range refs {
			if !ids[ref] {
				return newWireError(errValidation, "Error in expression '"+queries[i].ID+"': Unrecognized metric '"+ref+"'.")
			}
		}
	}

	return nil
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
