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

// cwParams is a Maximum > 5 CloudWatch alarm with the evaluation range on.
func cwParams(evalPeriods, datapointsToAlarm int, treat string) alarmeval.Params {
	return alarmeval.Params{
		Period: tablePeriod, EvaluationPeriods: evalPeriods, DatapointsToAlarm: datapointsToAlarm,
		Stat: "Maximum", ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		TreatMissingData: treat, ExtendedRange: true,
	}
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
				p := cwParams(3, datapointsToAlarm, treat)

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
		p := cwParams(3, 3, "")

		got := alarmeval.EvaluateWindow(datumsFor(points, now), &p, now)
		assert.NotEqual(t, alarmeval.StateAlarm, stateOf(got), points)
	}
}

// Data older than the evaluation range is not seen, so an alarm whose data
// stopped long ago has nothing left to evaluate.
func TestMissingDataOutsideRangeIsIgnored(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	p := cwParams(3, 0, "")

	got := alarmeval.EvaluateWindow(datumsFor("X X X - - - - -", now), &p, now)
	assert.Equal(t, alarmeval.StateInsufficientData, got.State)

	require.Equal(t, now.Add(-5*tablePeriod*time.Second), p.WindowStart(now), "range is N+2 periods")
}

// The reason data lists the points that were evaluated: the most recent
// Evaluation Periods real points, oldest first, reaching back into the range.
func TestRecentDatapointsReachBackIntoRange(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	p := cwParams(3, 0, "")

	assert.Equal(t, []float64{9, 9, 9}, alarmeval.RecentDatapoints(datumsFor("0 X X - X", now), &p, now))
	assert.Equal(t, []float64{9}, alarmeval.RecentDatapoints(datumsFor("- - X - -", now), &p, now))
}

// Enough breaching points alarm under every policy, before the premature rule
// can keep an "ignore" alarm's state. An AWS/DynamoDB ThrottledRequests alarm
// (ignore by default, M=1) must fire on its first breaching point.
func TestMissingDataEnoughBreachesAlarmUnderIgnore(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()

	rows := []struct {
		points string
		n, m   int
	}{
		{"- - - - X", 3, 1},
		{"- - X - X", 3, 2},
		{"- - - X X", 3, 2},
		{"- - - - X X X", 5, 3},
	}

	for _, row := range rows {
		for _, treat := range []string{"ignore", "missing"} {
			p := cwParams(row.n, row.m, treat)
			got := alarmeval.EvaluateWindow(datumsFor(row.points, now), &p, now)
			assert.Equal(t, alarmeval.StateAlarm, stateOf(got), "%s N=%d M=%d %s", row.points, row.n, row.m, treat)
		}
	}

	ddb := cwParams(3, 1, "")
	ddb.IgnoreMissingByDefault = true
	assert.Equal(t, alarmeval.StateAlarm, stateOf(alarmeval.EvaluateWindow(datumsFor("- - - - X", now), &ddb, now)))
}

// Without ExtendedRange (Azure windowSize, GCP duration) an evaluation sees
// exactly EvaluationPeriods periods and the premature rule is off.
func TestMissingDataWithoutExtendedRange(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()
	period := tablePeriod * time.Second

	p := cwParams(3, 3, "")
	p.ExtendedRange = false

	assert.Equal(t, alarmeval.StateOK, alarmeval.EvaluateWindow(datumsFor("- - X - -", now), &p, now).State)
	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(datumsFor("X X X - - -", now), &p, now).State)
	assert.Equal(t, now.Add(-3*period), p.WindowStart(now))
	assert.Equal(t, now.Add(-3*period), alarmeval.EvaluatedStart(datumsFor("- - X - -", now), &p, now))
}

// EvaluatedStart is the start of the oldest evaluated period: EvaluationPeriods
// back, or the oldest real point reached back into the range.
func TestEvaluatedStart(t *testing.T) {
	now := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)).Now()
	p := cwParams(3, 0, "")
	period := tablePeriod * time.Second

	assert.Equal(t, now.Add(-3*period), alarmeval.EvaluatedStart(nil, &p, now))
	assert.Equal(t, now.Add(-3*period), alarmeval.EvaluatedStart(datumsFor("X X X X X", now), &p, now))
	assert.Equal(t, now.Add(-4*period), alarmeval.EvaluatedStart(datumsFor("0 X - X X", now), &p, now))
	assert.Equal(t, now.Add(-5*period), alarmeval.EvaluatedStart(datumsFor("X - - - -", now), &p, now))
}
