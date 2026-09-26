package alarmeval_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// The band operators compare each period with its band point. A period with
// data but no band point is missing. Before CW-7a they never breached.
func TestEvaluateWindowBand(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mid := now.Add(-30 * time.Second)
	band := []alarmeval.BandPoint{{Timestamp: mid, Lower: 5, Upper: 15}}

	tests := []struct {
		name  string
		op    string
		value float64
		band  []alarmeval.BandPoint
		treat string
		want  string
	}{
		{"inside", "LessThanLowerOrGreaterThanUpperThreshold", 10, band, "", alarmeval.StateOK},
		{"above outside", "LessThanLowerOrGreaterThanUpperThreshold", 20, band, "", alarmeval.StateAlarm},
		{"below outside", "LessThanLowerOrGreaterThanUpperThreshold", 1, band, "", alarmeval.StateAlarm},
		{"above upper", "GreaterThanUpperThreshold", 20, band, "", alarmeval.StateAlarm},
		{"below upper", "GreaterThanUpperThreshold", 1, band, "", alarmeval.StateOK},
		{"below lower", "LessThanLowerThreshold", 1, band, "", alarmeval.StateAlarm},
		{"above lower", "LessThanLowerThreshold", 20, band, "", alarmeval.StateOK},
		{"on the edge", "GreaterThanUpperThreshold", 15, band, "", alarmeval.StateOK},
		{"no band", "GreaterThanUpperThreshold", 20, nil, "", alarmeval.StateInsufficientData},
		{"no band breaching", "GreaterThanUpperThreshold", 20, nil, "breaching", alarmeval.StateAlarm},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &alarmeval.Params{
				Period: 60, EvaluationPeriods: 1, ComparisonOperator: tc.op, Band: tc.band, TreatMissingData: tc.treat,
			}
			datums := []driver.MetricDatum{{Value: tc.value, Timestamp: mid}}

			assert.Equal(t, tc.want, alarmeval.EvaluateWindow(datums, p, now).State)
		})
	}
}

func TestIsBandOperatorAndRecentBand(t *testing.T) {
	assert.True(t, alarmeval.IsBandOperator("LessThanLowerThreshold"))
	assert.False(t, alarmeval.IsBandOperator("GreaterThanThreshold"))
	assert.False(t, alarmeval.EvaluateComparison(100, "GreaterThanUpperThreshold", 0))

	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	p := &alarmeval.Params{
		Period: 60, EvaluationPeriods: 3, ComparisonOperator: "GreaterThanUpperThreshold",
		Band: []alarmeval.BandPoint{
			{Timestamp: now.Add(-150 * time.Second), Lower: 1, Upper: 2},
			{Timestamp: now.Add(-30 * time.Second), Lower: 3, Upper: 4},
		},
	}
	datums := []driver.MetricDatum{
		{Value: 1, Timestamp: now.Add(-150 * time.Second)},
		{Value: 1, Timestamp: now.Add(-90 * time.Second)},
		{Value: 1, Timestamp: now.Add(-30 * time.Second)},
	}

	lower, upper := alarmeval.RecentBand(datums, p, now)
	assert.Equal(t, []float64{1, 3}, lower)
	assert.Equal(t, []float64{2, 4}, upper)
}
