package cloudwatch

import (
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// This file holds the PutMetricAlarm checks for Statistic, ExtendedStatistic,
// EvaluateLowSampleCountPercentile and EvaluationWindow, and the wire shapes of
// EvaluationWindow. Both codecs call them through putMetricAlarmCore.

// EvaluateLowSampleCountPercentile values.
const (
	lowSampleEvaluate = "evaluate"
	lowSampleIgnore   = "ignore"
)

// validateAlarmStatistic applies the PutMetricAlarm rule for an alarm on
// MetricName: "you must specify either Statistic or ExtendedStatistic but not
// both". ExtendedStatistic must follow the extended statistic grammar.
func validateAlarmStatistic(cfg *mondriver.AlarmConfig) error {
	if len(cfg.Metrics) > 0 || cfg.MetricName == "" {
		return nil
	}

	switch {
	case cfg.Stat != "" && cfg.ExtendedStatistic != "":
		return newWireError(errInvalidParameterCombo, "Exactly one of Statistic or ExtendedStatistic must be specified.")
	case cfg.Stat == "" && cfg.ExtendedStatistic == "":
		return newWireError(errValidation, "Exactly one of Statistic or ExtendedStatistic must be specified.")
	case cfg.Stat != "" && !validStatistics[cfg.Stat]:
		return newWireError(errValidation, "1 validation error detected: Value '"+cfg.Stat+
			"' at 'statistic' failed to satisfy constraint: Member must satisfy enum value set: "+
			"[Maximum, SampleCount, Sum, Minimum, Average]")
	case cfg.ExtendedStatistic != "":
		if _, err := alarmeval.ParseExtendedStatistic(cfg.ExtendedStatistic); err != nil {
			return newWireError(errValidation, "The value "+cfg.ExtendedStatistic+
				" for parameter ExtendedStatistic is not supported.")
		}
	}

	return nil
}

// validStatistics is the closed Statistic enum.
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validStatistics = map[string]bool{
	statSampleCount: true, statAverage: true, statSum: true, statMinimum: true, statMaximum: true,
}

// How far back, in seconds, EvaluationPeriods * Period may reach: one day
// for a Period under an hour, seven days for a Period of an hour or more.
const (
	maxAlarmLookbackDay  = 86400
	maxAlarmLookbackWeek = 604800
	hourPeriod           = 3600
)

// validateAlarmPeriods checks Period and DatapointsToAlarm. A single-metric
// alarm's Period is 10, 20, 30 or a multiple of 60, and EvaluationPeriods *
// Period is capped at a day, or at a week when Period is an hour or more.
func validateAlarmPeriods(cfg *mondriver.AlarmConfig) error {
	if cfg.DatapointsToAlarm > 0 && cfg.EvaluationPeriods > 0 && cfg.DatapointsToAlarm > cfg.EvaluationPeriods {
		return newWireError(errValidation, "DatapointsToAlarm must be less than or equal to EvaluationPeriods.")
	}

	if len(cfg.Metrics) > 0 || cfg.Period == 0 {
		return nil
	}

	if !validPeriod(cfg.Period) {
		return newWireError(errValidation, "Period must be 10, 20, 30 or a multiple of 60")
	}

	return validateAlarmLookback(cfg.Period, cfg.EvaluationPeriods)
}

// validateAlarmLookback checks EvaluationPeriods * Period against the day or
// week cap that applies to period p.
func validateAlarmLookback(p, evaluationPeriods int) error {
	lookback := p * max(evaluationPeriods, 1)

	switch {
	case p < hourPeriod && lookback > maxAlarmLookbackDay:
		return newWireError(errValidation, "Metrics cannot be checked across more than a day "+
			"(EvaluationPeriods * Period must be <= 86400)")
	case p >= hourPeriod && lookback > maxAlarmLookbackWeek:
		return newWireError(errValidation, "Metrics cannot be checked across more than a week "+
			"(EvaluationPeriods * Period must be <= 604800)")
	}

	return nil
}

// validateLowSample checks EvaluateLowSampleCountPercentile.
func validateLowSample(v string) error {
	switch v {
	case "", lowSampleEvaluate, lowSampleIgnore:
		return nil
	default:
		return newWireError(errValidation, "1 validation error detected: Value '"+v+
			"' at 'evaluateLowSampleCountPercentile' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [evaluate, ignore]")
	}
}

// evaluationWindowInput is a decoded EvaluationWindow union. Both codecs fill
// it. A nil pointer means the request left EvaluationWindow out.
type evaluationWindowInput struct {
	sliding  bool
	wall     bool
	timezone string
}

// applyEvaluationWindow validates the window and stores it on cfg. A wall
// clock window needs a supported period and a valid time zone.
func applyEvaluationWindow(cfg *mondriver.AlarmConfig, in *evaluationWindowInput) error {
	if in == nil {
		return nil
	}

	if in.sliding == in.wall {
		return newWireError(errValidation, "EvaluationWindow must specify exactly one of SlidingWindow or WallClockWindow.")
	}

	if in.sliding {
		cfg.EvaluationWindow = &mondriver.EvaluationWindow{}

		return nil
	}

	for _, p := range alarmPeriods(cfg) {
		if !alarmeval.WallClockPeriod(p) {
			return newWireError(errValidation, "The period "+strconv.Itoa(p)+" is not supported with a WallClockWindow. "+
				"Supported periods are 60, 300, 3600, 86400 and 604800 seconds.")
		}
	}

	if _, err := alarmeval.ParseTimezone(in.timezone); err != nil {
		return newWireError(errValidation, "Invalid WallClockWindow Timezone '"+in.timezone+
			"'. Specify an IANA time zone or a UTC offset that is a multiple of 5 minutes.")
	}

	cfg.EvaluationWindow = &mondriver.EvaluationWindow{WallClock: true, Timezone: in.timezone}

	return nil
}

// alarmPeriods lists the periods an alarm evaluates: its Period, or the
// Period of each MetricStat in a metric-math alarm.
func alarmPeriods(cfg *mondriver.AlarmConfig) []int {
	if len(cfg.Metrics) == 0 {
		return []int{cfg.Period}
	}

	var out []int

	for i := range cfg.Metrics {
		if ms := cfg.Metrics[i].MetricStat; ms != nil {
			out = append(out, ms.Period)
		}
	}

	return out
}

// wallClockWindowCBR is the WallClockWindow member of the union.
type wallClockWindowCBR struct {
	Timezone string `cbor:"Timezone,omitempty"`
}

// slidingWindowCBR is the empty SlidingWindow member of the union.
type slidingWindowCBR struct{}

// evaluationWindowCBR is the EvaluationWindow union on the CBOR wire.
type evaluationWindowCBR struct {
	SlidingWindow   *slidingWindowCBR   `cbor:"SlidingWindow,omitempty"`
	WallClockWindow *wallClockWindowCBR `cbor:"WallClockWindow,omitempty"`
}

func (w *evaluationWindowCBR) input() *evaluationWindowInput {
	if w == nil {
		return nil
	}

	in := &evaluationWindowInput{sliding: w.SlidingWindow != nil, wall: w.WallClockWindow != nil}
	if w.WallClockWindow != nil {
		in.timezone = w.WallClockWindow.Timezone
	}

	return in
}

func toEvaluationWindowCBR(w *mondriver.EvaluationWindow) *evaluationWindowCBR {
	switch {
	case w == nil:
		return nil
	case w.WallClock:
		return &evaluationWindowCBR{WallClockWindow: &wallClockWindowCBR{Timezone: w.Timezone}}
	default:
		return &evaluationWindowCBR{SlidingWindow: &slidingWindowCBR{}}
	}
}

// wallClockWindowXML is the WallClockWindow member in query XML.
type wallClockWindowXML struct {
	Timezone string `xml:"Timezone,omitempty"`
}

// evaluationWindowXML is the EvaluationWindow union in query XML.
type evaluationWindowXML struct {
	SlidingWindow   *struct{}           `xml:"SlidingWindow"`
	WallClockWindow *wallClockWindowXML `xml:"WallClockWindow"`
}

func toEvaluationWindowXML(w *mondriver.EvaluationWindow) *evaluationWindowXML {
	switch {
	case w == nil:
		return nil
	case w.WallClock:
		return &evaluationWindowXML{WallClockWindow: &wallClockWindowXML{Timezone: w.Timezone}}
	default:
		return &evaluationWindowXML{SlidingWindow: &struct{}{}}
	}
}
