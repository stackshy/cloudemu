package monitoring

import (
	"regexp"
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

func alarmSpec(compartmentID, name, query string) driver.OCIAlarmSpec {
	return driver.OCIAlarmSpec{
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
			metrics, err := m.ListOCIMetrics(t.Context(), tc.compartmentID, driver.OCIMetricFilter{})
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

	_, err := m.ListOCIMetrics(t.Context(), "", driver.OCIMetricFilter{})
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
			out, err := m.SummarizeOCIMetrics(t.Context(), compartmentA, driver.OCIMetricQuery{
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

	updated, err := m.UpdateOCIAlarm(ctx, created.ID, driver.OCIAlarmSpec{
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
			_, e := m.UpdateOCIAlarm(ctx, "ocid1.alarm.oc1.iad.missing", driver.OCIAlarmSpec{})
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
		{"no comparison", "CpuUtilization[1m].mean()", false, "", "", "", 0, 0},
		{"no interval", "CpuUtilization.mean() > 1", false, "", "", "", 0, 0},
		{"garbage", "hello", false, "", "", "", 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cond, ok := parseQuery(tc.query)
			require.Equal(t, tc.ok, ok)

			if !tc.ok {
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
		ComparisonOperator: "LessThanOrEqualToThreshold",
		Threshold:          12.5,
		Period:             300,
		Stat:               "Maximum",
	}

	query := formatQuery(&cfg)
	assert.Equal(t, "CpuUtilization[5m].max() <= 12.5", query)

	cond, ok := parseQuery(query)
	require.True(t, ok)
	assert.Equal(t, cfg.ComparisonOperator, cond.operator)
	assert.InDelta(t, cfg.Threshold, cond.threshold, 0.001)
}
