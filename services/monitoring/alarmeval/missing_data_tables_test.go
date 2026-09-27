package alarmeval_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retain marks a "Retain current state" cell of the doc tables.
const retain = "RETAIN"

const tablePeriod = 60

// missingDataRow is one row of a table in "How alarm state is evaluated when
// data is missing" (alarms-and-missing-data.html). The points run oldest to
// newest, left to right, as in the doc: 0 is not breaching, X is breaching
// and - is missing.
type missingDataRow struct {
	points                                  string
	missing, ignore, breaching, notBreached string
}

// datumsFor lays out one datum per real point, in the middle of its period.
func datumsFor(points string, now time.Time) []driver.MetricDatum {
	fields := strings.Fields(points)

	var out []driver.MetricDatum

	for i, f := range fields {
		age := len(fields) - 1 - i
		ts := now.Add(-time.Duration(age)*tablePeriod*time.Second - tablePeriod*time.Second/2)

		switch f {
		case "X":
			out = append(out, driver.MetricDatum{Value: 9, Timestamp: ts})
		case "0":
			out = append(out, driver.MetricDatum{Value: 1, Timestamp: ts})
		}
	}

	return out
}

func stateOf(out alarmeval.Outcome) string {
	if out.Retain {
		return retain
	}

	return out.State
}

func runMissingDataTable(t *testing.T, datapointsToAlarm int, rows []missingDataRow) {
	t.Helper()

	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	for _, row := range rows {
		cells := map[string]string{
			"missing": row.missing, "ignore": row.ignore,
			"breaching": row.breaching, "notBreaching": row.notBreached,
		}

		for treat, want := range cells {
			t.Run(row.points+"/"+treat, func(t *testing.T) {
				p := alarmeval.Params{
					Period: tablePeriod, EvaluationPeriods: 3, DatapointsToAlarm: datapointsToAlarm,
					Stat: "Maximum", ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
					TreatMissingData: treat,
				}

				got := alarmeval.EvaluateWindow(datumsFor(row.points, now), &p, now)
				assert.Equal(t, want, stateOf(got))
			})
		}
	}
}

// The first doc table: Datapoints to Alarm and Evaluation Periods are both 3,
// and the evaluation range is 5.
func TestMissingDataTableThreeOfThree(t *testing.T) {
	runMissingDataTable(t, 3, []missingDataRow{
		{"0 - X - X", "OK", "OK", "OK", "OK"},
		{"0 - - - -", "OK", "OK", "OK", "OK"},
		{"- - - - -", "INSUFFICIENT_DATA", retain, "ALARM", "OK"},
		{"0 X X - X", "ALARM", "ALARM", "ALARM", "ALARM"},
		{"- - X - -", "ALARM", retain, "ALARM", "OK"},
	})
}

// The second doc table: a 2 out of 3 alarm with an evaluation range of 5.
func TestMissingDataTableTwoOfThree(t *testing.T) {
	runMissingDataTable(t, 2, []missingDataRow{
		{"0 - X - X", "ALARM", "ALARM", "ALARM", "ALARM"},
		{"0 0 X 0 X", "ALARM", "ALARM", "ALARM", "ALARM"},
		{"0 - X - -", "OK", "OK", "ALARM", "OK"},
		{"- - - - 0", "OK", "OK", "ALARM", "OK"},
		{"- - - X -", "ALARM", retain, "ALARM", "OK"},
	})
}

// "the alarm does not go immediately into ALARM state when the data is either
// - - - - X or - - - X - and Datapoints to Alarm is 3."
func TestMissingDataRecentBreachIsNotPremature(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	for _, points := range []string{"- - - - X", "- - - X -"} {
		p := alarmeval.Params{
			Period: tablePeriod, EvaluationPeriods: 3, DatapointsToAlarm: 3,
			Stat: "Maximum", ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		}

		got := alarmeval.EvaluateWindow(datumsFor(points, now), &p, now)
		assert.NotEqual(t, alarmeval.StateAlarm, stateOf(got), points)
	}
}

// Data older than the evaluation range is not seen, so an alarm whose data
// stopped long ago has nothing left to evaluate.
func TestMissingDataOutsideRangeIsIgnored(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	p := alarmeval.Params{
		Period: tablePeriod, EvaluationPeriods: 3, Stat: "Maximum",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
	}

	got := alarmeval.EvaluateWindow(datumsFor("X X X - - - - -", now), &p, now)
	assert.Equal(t, alarmeval.StateInsufficientData, got.State)

	require.Equal(t, now.Add(-5*tablePeriod*time.Second), p.WindowStart(now), "range is N+2 periods")
}

// The reason data lists the points that were evaluated: the most recent
// Evaluation Periods real points, oldest first, reaching back into the range.
func TestRecentDatapointsReachBackIntoRange(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	p := alarmeval.Params{Period: tablePeriod, EvaluationPeriods: 3, Stat: "Maximum"}

	assert.Equal(t, []float64{9, 9, 9}, alarmeval.RecentDatapoints(datumsFor("0 X X - X", now), &p, now))
	assert.Equal(t, []float64{9}, alarmeval.RecentDatapoints(datumsFor("- - X - -", now), &p, now))
}
