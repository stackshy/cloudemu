package monitoring

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	compartmentA = "ocid1.compartment.oc1..aaaaaaaacompartmenta"
	compartmentB = "ocid1.compartment.oc1..aaaaaaaacompartmentb"
	namespace    = "cloudemu_app"
)

func newMock(t *testing.T) (*Mock, time.Time) {
	t.Helper()

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	opts := config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
		config.WithClock(config.NewFakeClock(now)),
	)

	return New(opts), now
}

func post(t *testing.T, m *Mock, compartmentID, name string, at time.Time, values ...float64) {
	t.Helper()

	data := make([]driver.MetricDatum, 0, len(values))
	for i, v := range values {
		data = append(data, driver.MetricDatum{
			Namespace:  namespace,
			MetricName: name,
			Value:      v,
			Dimensions: map[string]string{"resourceId": "vm-1"},
			Timestamp:  at.Add(time.Duration(i) * time.Second),
		})
	}

	require.NoError(t, m.PostMetricData(t.Context(), compartmentID, "", data))
}

func alarmSpec(compartmentID, name, query string) OCIAlarmSpec {
	return OCIAlarmSpec{
		DisplayName:   name,
		CompartmentID: compartmentID,
		Namespace:     namespace,
		Query:         query,
		Severity:      "CRITICAL",
		Destinations:  []string{"ocid1.onstopic.oc1.iad.aaaa"},
		IsEnabled:     true,
	}
}

func TestPostMetricDataValidation(t *testing.T) {
	m, _ := newMock(t)

	tests := []struct {
		name          string
		compartmentID string
		data          []driver.MetricDatum
		wantCode      cerrors.Code
	}{
		{"missing compartment", "", []driver.MetricDatum{{Namespace: namespace, MetricName: "cpu"}}, cerrors.InvalidArgument},
		{"no data", compartmentA, nil, cerrors.InvalidArgument},
		{"accepted", compartmentA, []driver.MetricDatum{{Namespace: namespace, MetricName: "cpu", Value: 1}}, cerrors.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := m.PostMetricData(t.Context(), tc.compartmentID, "", tc.data)
			assert.Equal(t, tc.wantCode, cerrors.GetCode(err))
		})
	}
}

func TestListOCIMetricsFiltersByCompartment(t *testing.T) {
	m, now := newMock(t)

	post(t, m, compartmentA, "CpuUtilization", now, 10)
	post(t, m, compartmentB, "MemoryUtilization", now, 20)

	tests := []struct {
		name          string
		compartmentID string
		want          []string
	}{
		{"compartment A sees only its own", compartmentA, []string{"CpuUtilization"}},
		{"compartment B sees only its own", compartmentB, []string{"MemoryUtilization"}},
		{"unrelated compartment sees nothing", "ocid1.compartment.oc1..aaaaaaaaother", []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics, err := m.ListOCIMetrics(t.Context(), tc.compartmentID, OCIMetricFilter{})
			require.NoError(t, err)

			names := make([]string, 0, len(metrics))
			for _, mt := range metrics {
				names = append(names, mt.Name)
			}

			assert.Equal(t, tc.want, names, "compartment %s", tc.compartmentID)
		})
	}
}

func TestListOCIMetricsRequiresCompartment(t *testing.T) {
	m, _ := newMock(t)

	_, err := m.ListOCIMetrics(t.Context(), "", OCIMetricFilter{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestSummarizeOCIMetrics(t *testing.T) {
	m, now := newMock(t)
	post(t, m, compartmentA, "CpuUtilization", now.Add(-30*time.Second), 10, 20, 30)

	tests := []struct {
		name       string
		query      string
		wantCode   cerrors.Code
		wantValues []float64
	}{
		{"mean", "CpuUtilization[1m].mean()", cerrors.OK, []float64{20}},
		{"sum", "CpuUtilization[1m].sum()", cerrors.OK, []float64{60}},
		{"max", "CpuUtilization[1m].max()", cerrors.OK, []float64{30}},
		{"unknown metric", "Nope[1m].mean()", cerrors.OK, nil},
		{"malformed", "not a query", cerrors.InvalidArgument, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := m.SummarizeOCIMetrics(t.Context(), compartmentA, OCIMetricQuery{
				Namespace: namespace,
				Query:     tc.query,
				StartTime: now.Add(-time.Minute),
				EndTime:   now.Add(time.Minute),
			})

			require.Equal(t, tc.wantCode, cerrors.GetCode(err))

			if tc.wantValues == nil {
				assert.Empty(t, out)
				return
			}

			require.Len(t, out, 1)
			assert.Equal(t, tc.wantValues, out[0].Values)
			assert.Equal(t, compartmentA, out[0].CompartmentID)
		})
	}
}

func TestAlarmCRUD(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)
	assert.Equal(t, StatusOK, created.Status)
	assert.Equal(t, lifecycleActive, created.LifecycleState)

	got, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "high-cpu", got.Spec.DisplayName)

	updated, err := m.UpdateOCIAlarm(ctx, created.ID, OCIAlarmSpec{
		DisplayName:   "high-cpu",
		CompartmentID: compartmentB, // ignored: the compartment is fixed at create
		Namespace:     namespace,
		Query:         "CpuUtilization[1m].mean() > 90",
		IsEnabled:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, compartmentA, updated.Spec.CompartmentID)
	assert.Equal(t, "CpuUtilization[1m].mean() > 90", updated.Spec.Query)

	require.NoError(t, m.DeleteOCIAlarm(ctx, created.ID))

	_, err = m.GetOCIAlarm(ctx, created.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestAlarmErrorPaths(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	_, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "dupe", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	tests := []struct {
		name string
		run  func() error
		want cerrors.Code
	}{
		{"duplicate display name", func() error {
			_, e := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "dupe", "CpuUtilization[1m].mean() > 80"))
			return e
		}, cerrors.AlreadyExists},
		{"same name in another compartment is fine", func() error {
			_, e := m.CreateOCIAlarm(ctx, alarmSpec(compartmentB, "dupe", "CpuUtilization[1m].mean() > 80"))
			return e
		}, cerrors.OK},
		{"missing display name", func() error {
			_, e := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "", "CpuUtilization[1m].mean() > 80"))
			return e
		}, cerrors.InvalidArgument},
		{"missing query", func() error {
			_, e := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "no-query", ""))
			return e
		}, cerrors.InvalidArgument},
		{"get missing", func() error {
			_, e := m.GetOCIAlarm(ctx, "ocid1.alarm.oc1.iad.missing")
			return e
		}, cerrors.NotFound},
		{"update missing", func() error {
			_, e := m.UpdateOCIAlarm(ctx, "ocid1.alarm.oc1.iad.missing", OCIAlarmSpec{})
			return e
		}, cerrors.NotFound},
		{"delete missing", func() error {
			return m.DeleteOCIAlarm(ctx, "ocid1.alarm.oc1.iad.missing")
		}, cerrors.NotFound},
		{"history of missing", func() error {
			_, e := m.OCIAlarmHistory(ctx, "ocid1.alarm.oc1.iad.missing", 0)
			return e
		}, cerrors.NotFound},
		{"list without compartment", func() error {
			_, e := m.ListOCIAlarms(ctx, "")
			return e
		}, cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cerrors.GetCode(tc.run()))
		})
	}
}

func TestListOCIAlarmsFiltersByCompartment(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	_, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "in-a", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	_, err = m.CreateOCIAlarm(ctx, alarmSpec(compartmentB, "in-b", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	alarms, err := m.ListOCIAlarms(ctx, compartmentA)
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "in-a", alarms[0].Spec.DisplayName)
}

func TestAlarmOCIDShape(t *testing.T) {
	m, _ := newMock(t)

	created, err := m.CreateOCIAlarm(t.Context(), alarmSpec(compartmentA, "shape", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	assert.Regexp(t, regexp.MustCompile(`^ocid1\.alarm\.oc1\.iad\.a{8}[0-9a-f]{16}$`), created.ID)

	ch, err := m.CreateNotificationChannel(t.Context(), driver.NotificationChannelConfig{Name: "ops", Type: "email"})
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^ocid1\.onstopic\.oc1\.iad\.`), ch.ID)
}

func TestAlarmFiresOnPostedMetrics(t *testing.T) {
	m, now := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "high-cpu", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	post(t, m, compartmentA, "CpuUtilization", now.Add(-10*time.Second), 95)

	fired, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, fired.Status)
	assert.False(t, fired.TimeTriggered.IsZero())

	history, err := m.OCIAlarmHistory(ctx, created.ID, 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, StatusOK, history[0].OldState)
	assert.Equal(t, StatusFiring, history[0].NewState)

	// A metric posted in another compartment must not move this alarm.
	post(t, m, compartmentB, "CpuUtilization", now.Add(-5*time.Second), 1)

	still, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, still.Status)
}

func TestPortableAPI(t *testing.T) {
	m, now := newMock(t)
	ctx := t.Context()

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: namespace, MetricName: "Latency", Value: 40, Timestamp: now.Add(-30 * time.Second)},
		{Namespace: namespace, MetricName: "Latency", Value: 60, Timestamp: now.Add(-20 * time.Second)},
	}))

	names, err := m.ListMetrics(ctx, namespace)
	require.NoError(t, err)
	assert.Equal(t, []string{"Latency"}, names)

	result, err := m.GetMetricData(ctx, driver.GetMetricInput{
		Namespace:  namespace,
		MetricName: "Latency",
		StartTime:  now.Add(-time.Minute),
		EndTime:    now,
		Period:     60,
		Stat:       "Average",
	})
	require.NoError(t, err)
	assert.Equal(t, []float64{50}, result.Values)

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "slow",
		Namespace:          namespace,
		MetricName:         "Latency",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          100,
		Period:             60,
		Stat:               "Average",
	}))

	alarms, err := m.DescribeAlarms(ctx, []string{"slow"})
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "Latency", alarms[0].MetricName)
	assert.InDelta(t, 100.0, alarms[0].Threshold, 0.001)
	assert.Equal(t, "OK", alarms[0].State)

	require.NoError(t, m.SetAlarmState(ctx, "slow", "ALARM", "manual"))

	alarms, err = m.DescribeAlarms(ctx, nil)
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "ALARM", alarms[0].State)

	history, err := m.GetAlarmHistory(ctx, "slow", 10)
	require.NoError(t, err)
	assert.Len(t, history, 1)

	require.NoError(t, m.DeleteAlarm(ctx, "slow"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteAlarm(ctx, "slow")))
}

func TestNotificationChannels(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	created, err := m.CreateNotificationChannel(ctx, driver.NotificationChannelConfig{
		Name: "ops", Type: "email", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	got, err := m.GetNotificationChannel(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "ops", got.Name)

	all, err := m.ListNotificationChannels(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)

	require.NoError(t, m.DeleteNotificationChannel(ctx, created.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteNotificationChannel(ctx, created.ID)))

	_, err = m.CreateNotificationChannel(ctx, driver.NotificationChannelConfig{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		ok        bool
		metric    string
		stat      string
		operator  string
		threshold float64
		interval  time.Duration
	}{
		{"mean greater than", "CpuUtilization[1m].mean() > 80", true, "CpuUtilization", "Average", "GreaterThanThreshold", 80, time.Minute},
		{"sum at least", "Errors[5m].sum() >= 3", true, "Errors", "Sum", "GreaterThanOrEqualToThreshold", 3, 5 * time.Minute},
		{"min below", "Free[1h].min() < 2.5", true, "Free", "Minimum", "LessThanThreshold", 2.5, time.Hour},
		{"count not equal", "Hits[1m].count() != 0", true, "Hits", "SampleCount", "NotEqualToThreshold", 0, time.Minute},
		{"dimension predicate", `Cpu[1m]{resourceId = "vm-1"}.mean() > 80`, true,
			"Cpu", "Average", "GreaterThanThreshold", 80, time.Minute},
		{"inequality predicate", `Cpu[1m]{resourceId != "vm-1"}.mean() > 80`, true,
			"Cpu", "Average", "GreaterThanThreshold", 80, time.Minute},
		{"no comparison", "CpuUtilization[1m].mean()", false, "", "", "", 0, 0},
		{"no interval", "CpuUtilization.mean() > 1", false, "", "", "", 0, 0},
		{"unknown aggregation", "CpuUtilization[1m].p99() > 1", false, "", "", "", 0, 0},
		{"unclosed predicate", `Cpu[1m]{resourceId = "vm-1".mean() > 1`, false, "", "", "", 0, 0},
		{"operatorless predicate", `Cpu[1m]{resourceId}.mean() > 1`, false, "", "", "", 0, 0},
		{"regex predicate", `Cpu[1m]{resourceId =~ "vm-.*"}.mean() > 1`, false, "", "", "", 0, 0},
		{"negated regex predicate", `Cpu[1m]{resourceId !~ "vm-.*"}.mean() > 1`, false, "", "", "", 0, 0},
		{"garbage", "hello", false, "", "", "", 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cond, err := parseQuery(tc.query)
			require.Equal(t, tc.ok, err == nil)

			if !tc.ok {
				assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
				return
			}

			assert.Equal(t, tc.metric, cond.metricName)
			assert.Equal(t, tc.stat, cond.stat)
			assert.Equal(t, tc.operator, cond.operator)
			assert.InDelta(t, tc.threshold, cond.threshold, 0.001)
			assert.Equal(t, tc.interval, cond.interval)
		})
	}
}

func TestFormatQueryRoundTrips(t *testing.T) {
	cfg := driver.AlarmConfig{
		MetricName:         "CpuUtilization",
		Dimensions:         map[string]string{"resourceId": "vm-1", "az": "ad-1"},
		ComparisonOperator: "LessThanOrEqualToThreshold",
		Threshold:          12.5,
		Period:             300,
		Stat:               "Maximum",
	}

	query, err := formatQuery(&cfg)
	require.NoError(t, err)
	assert.Equal(t, `CpuUtilization[5m]{az = "ad-1", resourceId = "vm-1"}.max() <= 12.5`, query)

	cond, err := parseQuery(query)
	require.NoError(t, err)
	assert.Equal(t, cfg.ComparisonOperator, cond.operator)
	assert.InDelta(t, cfg.Threshold, cond.threshold, 0.001)
	assert.Equal(t, []dimensionPredicate{
		{key: "az", op: opEqual, value: "ad-1"},
		{key: "resourceId", op: opEqual, value: "vm-1"},
	}, cond.dimensions)
}

func TestFormatQueryRejectsUnusableAlarms(t *testing.T) {
	tests := []struct {
		name string
		cfg  driver.AlarmConfig
	}{
		{"unknown operator", driver.AlarmConfig{MetricName: "Cpu", ComparisonOperator: "SortOfAbove", Threshold: 1}},
		{"no operator", driver.AlarmConfig{MetricName: "Cpu", Threshold: 1}},
		{"NaN threshold", driver.AlarmConfig{
			MetricName: "Cpu", ComparisonOperator: "GreaterThanThreshold", Threshold: math.NaN(),
		}},
		{"infinite threshold", driver.AlarmConfig{
			MetricName: "Cpu", ComparisonOperator: "GreaterThanThreshold", Threshold: math.Inf(1),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := formatQuery(&tc.cfg)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

			m, _ := newMock(t)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(m.CreateAlarm(t.Context(), tc.cfg)))
		})
	}
}

// TestSummarizeResolutionFloor covers the resolutions real OCI rejects,
// including the sub-second one that truncated to a zero step and spun
// aggregate's bucket loop forever. It runs in its own goroutine so a regression
// fails the test instead of hanging the suite.
func TestSummarizeResolutionFloor(t *testing.T) {
	m, now := newMock(t)
	post(t, m, compartmentA, "CpuUtilization", now.Add(-30*time.Second), 10, 20)

	tests := []struct {
		name       string
		query      string
		resolution string
		wantCode   cerrors.Code
	}{
		{"sub-second selector interval", "CpuUtilization[500ms].mean()", "", cerrors.InvalidArgument},
		{"sub-second resolution override", "CpuUtilization[1m].mean()", "500ms", cerrors.InvalidArgument},
		{"sub-minute selector interval", "CpuUtilization[30s].mean()", "", cerrors.InvalidArgument},
		{"sub-minute resolution override", "CpuUtilization[1m].mean()", "30s", cerrors.InvalidArgument},
		{"the minimum itself is accepted", "CpuUtilization[1m].mean()", "1m", cerrors.OK},
		{"non-positive resolution falls back", "CpuUtilization[1m].mean()", "0s", cerrors.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)

			go func() {
				_, err := m.SummarizeOCIMetrics(t.Context(), compartmentA, OCIMetricQuery{
					Namespace:  namespace,
					Query:      tc.query,
					Resolution: tc.resolution,
					StartTime:  now.Add(-time.Minute),
					EndTime:    now,
				})
				done <- err
			}()

			select {
			case err := <-done:
				assert.Equal(t, tc.wantCode, cerrors.GetCode(err))
			case <-time.After(5 * time.Second):
				t.Fatal("SummarizeOCIMetrics did not return: sub-second resolution spun the bucket loop")
			}
		})
	}
}

// TestAggregateNeverSpinsOnZeroStep guards the loop itself, independent of the
// validation in front of it.
func TestAggregateNeverSpinsOnZeroStep(t *testing.T) {
	_, now := newMock(t)
	points := []metricPoint{{timestamp: now.Add(-30 * time.Second), value: 7}}

	done := make(chan int, 1)

	go func() {
		stamps, _ := aggregate(points, now.Add(-time.Minute), now, 0, statAverage)
		done <- len(stamps)
	}()

	select {
	case n := <-done:
		assert.Equal(t, 1, n)
	case <-time.After(5 * time.Second):
		t.Fatal("aggregate did not return with a zero step")
	}
}

// TestConcurrentUpdateAndPost races UpdateOCIAlarm's write of rec.spec against
// the evaluation PostMetricData drives. It fails under -race unless evaluation
// reads a copy of the spec taken under the lock.
func TestConcurrentUpdateAndPost(t *testing.T) {
	m, now := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "racy", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	const rounds = 200

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for i := range rounds {
			spec := alarmSpec(compartmentA, "racy", "CpuUtilization[1m].mean() > "+strconv.Itoa(i))
			spec.ResourceGroup = "rg-" + strconv.Itoa(i)

			_, e := m.UpdateOCIAlarm(ctx, created.ID, spec)
			assert.NoError(t, e)
		}
	}()

	go func() {
		defer wg.Done()

		for i := range rounds {
			data := []driver.MetricDatum{{
				Namespace:  namespace,
				MetricName: "CpuUtilization",
				Value:      float64(i),
				Timestamp:  now.Add(-time.Duration(i) * time.Second),
			}}
			assert.NoError(t, m.PostMetricData(ctx, compartmentA, "", data))
		}
	}()

	wg.Wait()

	_, err = m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
}

func TestAlarmScopedByDimensions(t *testing.T) {
	m, now := newMock(t)
	ctx := t.Context()

	spec := alarmSpec(compartmentA, "vm-1-cpu", `CpuUtilization[1m]{resourceId = "vm-1"}.mean() > 80`)

	created, err := m.CreateOCIAlarm(ctx, spec)
	require.NoError(t, err)

	// A hot sample on a different resource must not move an alarm scoped to vm-1.
	require.NoError(t, m.PostMetricData(ctx, compartmentA, "", []driver.MetricDatum{{
		Namespace:  namespace,
		MetricName: "CpuUtilization",
		Value:      99,
		Dimensions: map[string]string{"resourceId": "vm-2"},
		Timestamp:  now.Add(-10 * time.Second),
	}}))

	quiet, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusOK, quiet.Status)

	post(t, m, compartmentA, "CpuUtilization", now.Add(-5*time.Second), 95)

	fired, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, fired.Status)
}

// TestAlarmSeesSampleAtNow covers the evaluation window's upper bound: a sample
// posted at the clock's now must count toward its own evaluation.
func TestAlarmSeesSampleAtNow(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "now-cpu", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	// No timestamp, so the datum lands at exactly now.
	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: namespace, MetricName: "CpuUtilization", Value: 95},
	}))

	fired, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, fired.Status)
}

func TestDescribeAlarmsIsCompartmentScoped(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	_, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "in-default", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	_, err = m.CreateOCIAlarm(ctx, alarmSpec(compartmentB, "elsewhere", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	all, err := m.DescribeAlarms(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "in-default", all[0].Name)

	named, err := m.DescribeAlarms(ctx, []string{"elsewhere"})
	require.NoError(t, err)
	assert.Empty(t, named)
}

func TestDestinationsFoldsEveryActionList(t *testing.T) {
	cfg := driver.AlarmConfig{
		AlarmActions:            []string{"topic-a", "topic-b"},
		OKActions:               []string{"topic-a", "topic-c"},
		InsufficientDataActions: []string{"topic-d"},
	}

	assert.Equal(t, []string{"topic-a", "topic-b", "topic-c", "topic-d"}, destinations(&cfg))
	assert.Equal(t, []string{}, destinations(&driver.AlarmConfig{}))
}

// TestConcurrentCreateHasOneWinner races two creates of the same display name.
// Unless the uniqueness scan and the insert share one lock, both see the name
// absent and both insert, leaving two alarms a lookup by name resolves at random.
func TestConcurrentCreateHasOneWinner(t *testing.T) {
	const (
		rounds   = 200
		creators = 8
	)

	for i := range rounds {
		m, _ := newMock(t)
		name := "racy-" + strconv.Itoa(i)

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			winners int
		)

		// Both creates wait on the same gate, so they reach the uniqueness
		// scan together rather than one finishing before the other starts.
		gate := make(chan struct{})

		wg.Add(creators)

		for range creators {
			go func() {
				defer wg.Done()

				<-gate

				_, err := m.CreateOCIAlarm(t.Context(), alarmSpec(compartmentA, name, "CpuUtilization[1m].mean() > 80"))
				if err == nil {
					mu.Lock()
					winners++
					mu.Unlock()
				}
			}()
		}

		close(gate)
		wg.Wait()

		require.Equal(t, 1, winners, "exactly one create must win")

		alarms, err := m.ListOCIAlarms(t.Context(), compartmentA)
		require.NoError(t, err)
		require.Len(t, alarms, 1)
	}
}

// pendingMock builds a mock whose clock the caller can advance.
func pendingMock(t *testing.T) (*Mock, *config.FakeClock, time.Time) {
	t.Helper()

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	clock := config.NewFakeClock(now)

	return New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
		config.WithClock(clock),
	)), clock, now
}

// TestAlarmWaitsForPendingDuration covers pendingDuration: the breach must
// persist it before the alarm fires, rather than firing on the first breaching
// datapoint.
func TestAlarmWaitsForPendingDuration(t *testing.T) {
	m, clock, now := pendingMock(t)
	ctx := t.Context()

	spec := alarmSpec(compartmentA, "slow-burn", "CpuUtilization[1m].mean() > 80")
	spec.PendingDuration = "PT5M"

	created, err := m.CreateOCIAlarm(ctx, spec)
	require.NoError(t, err)

	post(t, m, compartmentA, "CpuUtilization", now.Add(-10*time.Second), 95)

	waiting, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusOK, waiting.Status, "one breaching datapoint must not fire a PT5M alarm")

	clock.Advance(6 * time.Minute)
	post(t, m, compartmentA, "CpuUtilization", clock.Now().UTC().Add(-10*time.Second), 95)

	fired, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFiring, fired.Status, "a breach older than the pending duration must fire")
}

// TestRecoveryRestartsPendingWindow covers the other half: a breach that
// recovers before the pending duration elapses starts the window over.
func TestRecoveryRestartsPendingWindow(t *testing.T) {
	m, clock, now := pendingMock(t)
	ctx := t.Context()

	spec := alarmSpec(compartmentA, "flapping", "CpuUtilization[1m].mean() > 80")
	spec.PendingDuration = "PT5M"

	created, err := m.CreateOCIAlarm(ctx, spec)
	require.NoError(t, err)

	post(t, m, compartmentA, "CpuUtilization", now.Add(-10*time.Second), 95)

	clock.Advance(4 * time.Minute)
	post(t, m, compartmentA, "CpuUtilization", clock.Now().UTC().Add(-10*time.Second), 5)

	clock.Advance(2 * time.Minute)
	post(t, m, compartmentA, "CpuUtilization", clock.Now().UTC().Add(-10*time.Second), 95)

	got, err := m.GetOCIAlarm(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusOK, got.Status, "the recovery restarted the pending window")
}

// TestEvaluationPeriodsBecomePendingDuration covers the portable alarm field
// that OCI expresses as pendingDuration.
func TestEvaluationPeriodsBecomePendingDuration(t *testing.T) {
	m, clock, _ := pendingMock(t)
	ctx := t.Context()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "slow",
		Namespace:          namespace,
		MetricName:         "Latency",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          100,
		Period:             60,
		EvaluationPeriods:  3,
		Stat:               "Average",
	}))

	alarms, err := m.ListOCIAlarms(ctx, compartmentA)
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "PT120S", alarms[0].Spec.PendingDuration)

	hot := []driver.MetricDatum{{Namespace: namespace, MetricName: "Latency", Value: 150}}

	require.NoError(t, m.PutMetricData(ctx, hot))

	waiting, err := m.DescribeAlarms(ctx, []string{"slow"})
	require.NoError(t, err)
	require.Len(t, waiting, 1)
	assert.Equal(t, "OK", waiting[0].State)

	clock.Advance(3 * time.Minute)
	require.NoError(t, m.PutMetricData(ctx, hot))

	fired, err := m.DescribeAlarms(ctx, []string{"slow"})
	require.NoError(t, err)
	require.Len(t, fired, 1)
	assert.Equal(t, "ALARM", fired[0].State)
}

func TestPendingOf(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"minutes", "PT5M", 5 * time.Minute},
		{"hours and minutes", "PT1H30M", 90 * time.Minute},
		{"seconds", "PT45S", 45 * time.Second},
		{"a day", "P1D", 24 * time.Hour},
		{"lower case", "pt2m", 2 * time.Minute},
		{"empty", "", 0},
		{"not a duration", "5m", 0},
		{"unit without a number", "PTM", 0},
		{"trailing digits", "PT5", 0},
		{"unknown unit", "PT5X", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pendingOf(tc.raw))
		})
	}
}

// TestDimensionPredicateOperators covers MQL's non-equality dimension
// predicates. "!=" selects the complementary series; the pattern operators are
// refused outright rather than misparsed into a silent empty result.
func TestDimensionPredicateOperators(t *testing.T) {
	m, now := newMock(t)
	ctx := t.Context()

	require.NoError(t, m.PostMetricData(ctx, compartmentA, "", []driver.MetricDatum{
		{
			Namespace: namespace, MetricName: "Cpu", Value: 10,
			Dimensions: map[string]string{"resourceId": "vm-1"}, Timestamp: now.Add(-30 * time.Second),
		},
		{
			Namespace: namespace, MetricName: "Cpu", Value: 90,
			Dimensions: map[string]string{"resourceId": "vm-2"}, Timestamp: now.Add(-30 * time.Second),
		},
	}))

	tests := []struct {
		name       string
		query      string
		wantCode   cerrors.Code
		wantValues []float64
	}{
		{"equality", `Cpu[1m]{resourceId = "vm-1"}.mean()`, cerrors.OK, []float64{10}},
		{"inequality", `Cpu[1m]{resourceId != "vm-1"}.mean()`, cerrors.OK, []float64{90}},
		{"pattern match", `Cpu[1m]{resourceId =~ "vm-.*"}.mean()`, cerrors.InvalidArgument, nil},
		{"negated pattern match", `Cpu[1m]{resourceId !~ "vm-.*"}.mean()`, cerrors.InvalidArgument, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := m.SummarizeOCIMetrics(ctx, compartmentA, OCIMetricQuery{
				Namespace: namespace,
				Query:     tc.query,
				StartTime: now.Add(-time.Minute),
				EndTime:   now,
			})

			require.Equal(t, tc.wantCode, cerrors.GetCode(err))

			if tc.wantValues == nil {
				assert.Empty(t, out)
				return
			}

			require.Len(t, out, 1)
			assert.Equal(t, tc.wantValues, out[0].Values)
		})
	}
}

// TestAlarmRejectsUnsupportedPredicate keeps an alarm from being stored with a
// predicate this parser refuses, which would leave it silently unable to fire.
func TestAlarmRejectsUnsupportedPredicate(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	_, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "pattern",
		`Cpu[1m]{resourceId =~ "vm-.*"}.mean() > 80`))
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "exact",
		`Cpu[1m]{resourceId != "vm-1"}.mean() > 80`))
	require.NoError(t, err)

	_, err = m.UpdateOCIAlarm(ctx, created.ID, alarmSpec(compartmentA, "exact",
		`Cpu[1m]{resourceId !~ "vm-.*"}.mean() > 80`))
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

// TestPostMetricDataShapeLimits covers the namespace, name and dimension
// formats real OCI enforces on ingestion.
func TestPostMetricDataShapeLimits(t *testing.T) {
	m, _ := newMock(t)

	tests := []struct {
		name     string
		datum    driver.MetricDatum
		wantCode cerrors.Code
	}{
		{"accepted", driver.MetricDatum{
			Namespace: namespace, MetricName: "Cpu", Dimensions: map[string]string{"resourceId": "vm-1"},
		}, cerrors.OK},
		{"missing namespace", driver.MetricDatum{MetricName: "Cpu"}, cerrors.InvalidArgument},
		{"namespace with a slash", driver.MetricDatum{
			Namespace: "app/metrics", MetricName: "Cpu",
		}, cerrors.InvalidArgument},
		{"namespace starting with a digit", driver.MetricDatum{
			Namespace: "1app", MetricName: "Cpu",
		}, cerrors.InvalidArgument},
		{"reserved namespace", driver.MetricDatum{
			Namespace: "oci_computeagent", MetricName: "Cpu",
		}, cerrors.InvalidArgument},
		{"namespace too long", driver.MetricDatum{
			Namespace: strings.Repeat("a", 256), MetricName: "Cpu",
		}, cerrors.InvalidArgument},
		{"missing metric name", driver.MetricDatum{Namespace: namespace}, cerrors.InvalidArgument},
		{"metric name too long", driver.MetricDatum{
			Namespace: namespace, MetricName: strings.Repeat("a", 256),
		}, cerrors.InvalidArgument},
		{"empty dimension value", driver.MetricDatum{
			Namespace: namespace, MetricName: "Cpu", Dimensions: map[string]string{"resourceId": ""},
		}, cerrors.InvalidArgument},
		{"dimension key with a period", driver.MetricDatum{
			Namespace: namespace, MetricName: "Cpu", Dimensions: map[string]string{"resource.id": "vm-1"},
		}, cerrors.InvalidArgument},
		{"dimension value too long", driver.MetricDatum{
			Namespace: namespace, MetricName: "Cpu",
			Dimensions: map[string]string{"resourceId": strings.Repeat("v", 257)},
		}, cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := m.PostMetricData(t.Context(), compartmentA, "", []driver.MetricDatum{tc.datum})
			assert.Equal(t, tc.wantCode, cerrors.GetCode(err))
		})
	}
}

// TestAlarmRejectsUnparseableQuery is the create-time guard on the query: an
// alarm evaluate could never read is refused rather than stored ACTIVE and
// silently inert.
func TestAlarmRejectsUnparseableQuery(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	queries := []string{
		"CpuUtilization.mean() > 80",       // no interval
		"CpuUtilization[1m].p99() > 80",    // an aggregation with no statistic
		"CpuUtilization[1m].mean()",        // no threshold comparison
		"CpuUtilization[1m].mean() > lots", // a threshold that is not a number
		"hello",
	}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			_, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "inert", query))
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}

	stored, err := m.ListOCIAlarms(ctx, compartmentA)
	require.NoError(t, err)
	assert.Empty(t, stored, "a refused alarm must not be stored")

	t.Run("update is guarded too", func(t *testing.T) {
		created, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "cpu", "CpuUtilization[1m].mean() > 80"))
		require.NoError(t, err)

		_, err = m.UpdateOCIAlarm(ctx, created.ID, alarmSpec(compartmentA, "cpu", "CpuUtilization.mean() > 90"))
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

		got, err := m.GetOCIAlarm(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "CpuUtilization[1m].mean() > 80", got.Spec.Query, "a refused update leaves the query alone")
	})
}

// TestAlarmFieldValidation covers the alarm values OCI constrains to an enum or
// a duration format, which are checked at create rather than ignored later.
func TestAlarmFieldValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OCIAlarmSpec)
		want   cerrors.Code
	}{
		{"unknown severity", func(s *OCIAlarmSpec) { s.Severity = "URGENT" }, cerrors.InvalidArgument},
		{"lower-case severity", func(s *OCIAlarmSpec) { s.Severity = "critical" }, cerrors.InvalidArgument},
		{"every severity OCI names", func(s *OCIAlarmSpec) { s.Severity = severityInfo }, cerrors.OK},
		{"pendingDuration that is not ISO-8601", func(s *OCIAlarmSpec) {
			s.PendingDuration = "5m"
		}, cerrors.InvalidArgument},
		{"pendingDuration with a unit but no number", func(s *OCIAlarmSpec) {
			s.PendingDuration = "PTM"
		}, cerrors.InvalidArgument},
		{"ISO-8601 pendingDuration", func(s *OCIAlarmSpec) { s.PendingDuration = "PT5M" }, cerrors.OK},
		{"resolution that is not an interval", func(s *OCIAlarmSpec) {
			s.Resolution = "hourly"
		}, cerrors.InvalidArgument},
		{"sub-minute resolution", func(s *OCIAlarmSpec) { s.Resolution = "30s" }, cerrors.InvalidArgument},
		{"minute resolution", func(s *OCIAlarmSpec) { s.Resolution = "5m" }, cerrors.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newMock(t)
			spec := alarmSpec(compartmentA, "alarm", "CpuUtilization[1m].mean() > 80")
			tc.mutate(&spec)

			_, err := m.CreateOCIAlarm(t.Context(), spec)
			assert.Equal(t, tc.want, cerrors.GetCode(err))
		})
	}

	m, _ := newMock(t)
	spec := alarmSpec(compartmentA, "defaulted", "CpuUtilization[1m].mean() > 80")
	spec.Severity = ""

	created, err := m.CreateOCIAlarm(t.Context(), spec)
	require.NoError(t, err)
	assert.Equal(t, severityWarning, created.Spec.Severity, "an omitted severity defaults")
	assert.Equal(t, defaultResolution, created.Spec.Resolution, "an omitted resolution defaults")
}

// TestUpdateRejectsDuplicateDisplayName holds an update to the uniqueness rule
// create enforces, so a rename cannot make the duplicate create refuses.
func TestUpdateRejectsDuplicateDisplayName(t *testing.T) {
	m, _ := newMock(t)
	ctx := t.Context()

	first, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "taken", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	second, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentA, "free", "CpuUtilization[1m].mean() > 80"))
	require.NoError(t, err)

	_, err = m.UpdateOCIAlarm(ctx, second.ID, alarmSpec(compartmentA, "taken", "CpuUtilization[1m].mean() > 90"))
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	got, err := m.GetOCIAlarm(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, "free", got.Spec.DisplayName, "a refused update leaves the alarm alone")
	assert.Equal(t, "CpuUtilization[1m].mean() > 80", got.Spec.Query)

	t.Run("an alarm keeps its own name", func(t *testing.T) {
		_, err := m.UpdateOCIAlarm(ctx, first.ID, alarmSpec(compartmentA, "taken", "CpuUtilization[1m].mean() > 95"))
		require.NoError(t, err)
	})

	t.Run("another compartment may hold the name", func(t *testing.T) {
		other, err := m.CreateOCIAlarm(ctx, alarmSpec(compartmentB, "free", "CpuUtilization[1m].mean() > 80"))
		require.NoError(t, err)

		_, err = m.UpdateOCIAlarm(ctx, other.ID, alarmSpec(compartmentB, "taken", "CpuUtilization[1m].mean() > 90"))
		require.NoError(t, err)
	})
}
