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
// request never reaches the driver. thresholdSet reports whether the request
// carried a Threshold.
func (h *Handler) putMetricAlarmCore(ctx context.Context, cfg *mondriver.AlarmConfig, thresholdSet bool) error {
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

	if err := validateAnomalyThreshold(cfg, thresholdSet); err != nil {
		return err
	}

	return h.monitoring.CreateAlarm(ctx, *cfg)
}

// floatOrZero returns *v, or 0 when v is nil.
func floatOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}

	return *v
}

// alarmThreshold is the Threshold DescribeAlarms returns. An anomaly alarm
// has a band instead, so it has none.
func alarmThreshold(a *mondriver.AlarmInfo) *float64 {
	if a.ThresholdMetricID != "" {
		return nil
	}

	v := a.Threshold

	return &v
}

// validateAnomalyThreshold checks the band operators. They compare against
// the ANOMALY_DETECTION_BAND entry named by ThresholdMetricId, and only they
// can use it.
func validateAnomalyThreshold(cfg *mondriver.AlarmConfig, thresholdSet bool) error {
	band := alarmeval.IsBandOperator(cfg.ComparisonOperator)

	switch {
	case band && cfg.ThresholdMetricID == "":
		return newWireError(errValidation, "ComparisonOperator "+cfg.ComparisonOperator+
			" can only be used with ThresholdMetricId.")
	case cfg.ThresholdMetricID == "":
		return nil
	case !band:
		return newWireError(errValidation, "ThresholdMetricId can only be used with the LessThanLowerOrGreaterThanUpperThreshold, "+
			"LessThanLowerThreshold or GreaterThanUpperThreshold comparison operators.")
	case thresholdSet:
		return newWireError(errValidation, "Threshold cannot be used with ThresholdMetricId.")
	}

	for i := range cfg.Metrics {
		if cfg.Metrics[i].ID != cfg.ThresholdMetricID {
			continue
		}

		if _, ok := metricmath.BandInput(cfg.Metrics[i].Expression); !ok {
			return newWireError(errValidation, "ThresholdMetricId "+cfg.ThresholdMetricID+
				" must name an ANOMALY_DETECTION_BAND expression.")
		}
	}

	return nil
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

	if err := validateQueryCounts(cfg.Metrics); err != nil {
		return err
	}

	ids, err := metricQueryIDs(cfg.Metrics)
	if err != nil {
		return err
	}

	return validateQueryLinks(cfg, ids)
}

// validateQueryLinks checks how the entries refer to each other: one watched
// entry, a known ThresholdMetricId, known references and no cycle.
func validateQueryLinks(cfg *mondriver.AlarmConfig, ids map[string]bool) error {
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

		if err := validateQueryShape(q); err != nil {
			return nil, err
		}
	}

	return ids, nil
}

// Limits on a PutMetricAlarm Metrics list.
const (
	maxAlarmMetricStats  = 10
	maxAlarmExpressions  = 10
	secondsPerMinute     = 60
	highResolutionPeriod = 30
	highResolutionStep   = 10
)

// validPeriod reports whether p is 10, 20, 30 or a multiple of 60.
func validPeriod(p int) bool {
	if p <= 0 {
		return false
	}

	if p <= highResolutionPeriod {
		return p%highResolutionStep == 0
	}

	return p%secondsPerMinute == 0
}

// validateQueryShape checks one Metrics entry. It has exactly one of
// MetricStat and Expression. A MetricStat needs a valid Period and a Stat.
// An Expression Period, when set, follows the same Period rule.
func validateQueryShape(q *mondriver.MetricDataQuery) error {
	if (q.MetricStat == nil) == (q.Expression == "") {
		return newWireError(errValidation, "Invalid metrics list: the element '"+q.ID+
			"' must specify exactly one of MetricStat and Expression.")
	}

	if ms := q.MetricStat; ms != nil {
		if !validPeriod(ms.Period) {
			return newWireError(errValidation, "Invalid metrics list: the element '"+q.ID+
				"' must have a MetricStat Period of 10, 20, 30 or a multiple of 60.")
		}

		if ms.Stat == "" {
			return newWireError(errValidation, "Invalid metrics list: the element '"+q.ID+"' must have a MetricStat Stat.")
		}
	}

	if q.Period != 0 && !validPeriod(q.Period) {
		return newWireError(errValidation, "Invalid metrics list: the element '"+q.ID+
			"' must have a Period of 10, 20, 30 or a multiple of 60.")
	}

	return nil
}

// validateQueryCounts enforces the per-alarm limits on MetricStat and
// Expression entries.
func validateQueryCounts(queries []mondriver.MetricDataQuery) error {
	stats, exprs := 0, 0

	for i := range queries {
		if queries[i].MetricStat != nil {
			stats++
		} else {
			exprs++
		}
	}

	if stats > maxAlarmMetricStats {
		return newWireError(errValidation, "The metrics list can contain at most 10 MetricStat elements.")
	}

	if exprs > maxAlarmExpressions {
		return newWireError(errValidation, "The metrics list can contain at most 10 Expression elements.")
	}

	return nil
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
