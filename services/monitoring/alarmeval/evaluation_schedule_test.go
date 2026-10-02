package alarmeval_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stretchr/testify/assert"
)

// Cadences from the CloudWatch alarm-evaluation guide.
func TestEvaluationInterval(t *testing.T) {
	tests := []struct {
		name        string
		period      int
		evalPeriods int
		want        time.Duration
	}{
		{name: "10s period", period: 10, evalPeriods: 3, want: 10 * time.Second},
		{name: "30s period", period: 30, evalPeriods: 1, want: 10 * time.Second},
		{name: "one minute", period: 60, evalPeriods: 5, want: time.Minute},
		{name: "defaults", want: time.Minute},
		{name: "exactly one day", period: 86400, evalPeriods: 1, want: time.Minute},
		{name: "over one day", period: 3600, evalPeriods: 25, want: time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, alarmeval.EvaluationInterval(tc.period, tc.evalPeriods))
		})
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	assert.True(t, alarmeval.Due(time.Time{}, now, time.Minute), "never evaluated")
	assert.False(t, alarmeval.Due(now.Add(-59*time.Second), now, time.Minute))
	assert.True(t, alarmeval.Due(now.Add(-time.Minute), now, time.Minute))
}

func TestEvaluationTime(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 34, 56, 0, time.UTC)

	daily := alarmeval.Params{Period: 3600, EvaluationPeriods: 25}
	assert.Equal(t, time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), daily.EvaluationTime(now))

	minute := alarmeval.Params{Period: 60, EvaluationPeriods: 5}
	assert.Equal(t, now, minute.EvaluationTime(now))
}

// With every period empty, each policy gives the result in the "- - - - -" row
// of the missing-data guide.
func TestEvaluateWindowAllMissing(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	base := alarmeval.Params{
		Period: 60, EvaluationPeriods: 3,
		Stat: "Average", ComparisonOperator: "GreaterThanThreshold", Threshold: 10,
	}

	tests := []struct {
		treat         string
		ignoreDefault bool
		want          alarmeval.Outcome
	}{
		{treat: "", want: alarmeval.Outcome{
			State: alarmeval.StateInsufficientData, Reason: "Insufficient Data: 3 datapoints were unknown.",
		}},
		{treat: "missing", want: alarmeval.Outcome{
			State: alarmeval.StateInsufficientData, Reason: "Insufficient Data: 3 datapoints were unknown.",
		}},
		{treat: "ignore", want: alarmeval.Outcome{Retain: true}},
		{treat: "breaching", want: alarmeval.Outcome{State: alarmeval.StateAlarm, Reason: "Threshold crossed"}},
		{treat: "notBreaching", want: alarmeval.Outcome{State: alarmeval.StateOK, Reason: "Threshold not crossed"}},
		{treat: "", ignoreDefault: true, want: alarmeval.Outcome{Retain: true}},
		{treat: "missing", ignoreDefault: true, want: alarmeval.Outcome{
			State: alarmeval.StateInsufficientData, Reason: "Insufficient Data: 3 datapoints were unknown.",
		}},
	}

	for _, tc := range tests {
		t.Run(tc.treat, func(t *testing.T) {
			p := base
			p.TreatMissingData = tc.treat
			p.IgnoreMissingByDefault = tc.ignoreDefault

			assert.Equal(t, tc.want, alarmeval.EvaluateWindow(nil, &p, now))
		})
	}

	one := alarmeval.Params{Period: 60, EvaluationPeriods: 1}
	assert.Equal(t, "Insufficient Data: 1 datapoint was unknown.", alarmeval.EvaluateWindow(nil, &one, now).Reason)
}
