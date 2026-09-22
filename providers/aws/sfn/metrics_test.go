package sfn_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

const metricsFailDefinition = `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"Boom","Cause":"c"}}}`

// sfnMetric returns the Sum of an AWS/States metric for smARN around clk.Now,
// and its unit.
func sfnMetric(t *testing.T, cw *cloudwatch.Mock, clk *config.FakeClock, smARN, name string) (float64, string) {
	t.Helper()

	res, err := cw.GetMetricData(context.Background(), mondriver.GetMetricInput{
		Namespace: "AWS/States", MetricName: name,
		Dimensions: map[string]string{"StateMachineArn": smARN},
		StartTime:  clk.Now().Add(-time.Hour), EndTime: clk.Now().Add(time.Hour),
		Period: 7200, Stat: "Sum",
	})
	if err != nil {
		t.Fatalf("GetMetricData %s: %v", name, err)
	}

	if len(res.Values) == 0 {
		return 0, res.Unit
	}

	return res.Values[0], res.Unit
}

func newMetricsMock(clk *config.FakeClock, extra ...config.Option) (*sfn.Mock, *cloudwatch.Mock) {
	opts := config.NewOptions(append([]config.Option{
		config.WithClock(clk), config.WithRegion("us-east-1"), config.WithAccountID("000000000000"),
	}, extra...)...)

	m := sfn.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	return m, cw
}

// TestExecutionMetrics pins the AWS/States execution metrics on the
// StateMachineArn dimension: ExecutionsStarted plus the terminal
// Executions<Status> count (Count) and ExecutionTime (Milliseconds).
func TestExecutionMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m, cw := newMetricsMock(clk)
	ctx := context.Background()

	okARN := createSM(t, m, "ok")

	failARN, _, _, err := m.CreateStateMachine(ctx, driver.CreateStateMachineInput{
		Name: "bad", Definition: metricsFailDefinition, RoleArn: "arn:aws:iam::000000000000:role/r",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	for _, arn := range []string{okARN, okARN, failARN} {
		if _, err = m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Input: "{}"}); err != nil {
			t.Fatalf("StartExecution: %v", err)
		}
	}

	checks := []struct {
		arn, name string
		want      float64
		unit      string
	}{
		{okARN, "ExecutionsStarted", 2, "Count"},
		{okARN, "ExecutionsSucceeded", 2, "Count"},
		{okARN, "ExecutionTime", 0, "Milliseconds"},
		{failARN, "ExecutionsStarted", 1, "Count"},
		{failARN, "ExecutionsFailed", 1, "Count"},
	}

	for _, c := range checks {
		got, unit := sfnMetric(t, cw, clk, c.arn, c.name)
		if got != c.want || unit != c.unit {
			t.Errorf("%s{%s} = %v %q, want %v %q", c.name, c.arn, got, unit, c.want, c.unit)
		}
	}

	if got, _ := sfnMetric(t, cw, clk, failARN, "ExecutionsSucceeded"); got != 0 {
		t.Errorf("failed machine must not record ExecutionsSucceeded, got %v", got)
	}
}

// TestExecutionMetricsAbortUnderSettle pins that under AsyncSettle a still-
// running execution records only ExecutionsStarted, and a StopExecution that
// aborts it records ExecutionsAborted — never a would-be success.
func TestExecutionMetricsAbortUnderSettle(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m, cw := newMetricsMock(clk, config.WithAsyncSettle())
	ctx := context.Background()
	arn := createSM(t, m, "sm")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Input: "{}"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if got, _ := sfnMetric(t, cw, clk, arn, "ExecutionsStarted"); got != 1 {
		t.Fatalf("ExecutionsStarted = %v, want 1", got)
	}

	if _, err = m.StopExecution(ctx, exec.ARN, "", ""); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}

	if got, _ := sfnMetric(t, cw, clk, arn, "ExecutionsAborted"); got != 1 {
		t.Errorf("ExecutionsAborted = %v, want 1", got)
	}

	if got, _ := sfnMetric(t, cw, clk, arn, "ExecutionsSucceeded"); got != 0 {
		t.Errorf("aborted run must not record ExecutionsSucceeded, got %v", got)
	}
}

// TestExecutionMetricsSettleNaturally pins that under AsyncSettle a run that
// settles on its own publishes its close metrics exactly once — at the first
// settled observation (Describe/List/History), stamped at its StopDate — and
// that later observations never re-publish.
func TestExecutionMetricsSettleNaturally(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m, cw := newMetricsMock(clk, config.WithAsyncSettle())
	ctx := context.Background()
	arn := createSM(t, m, "sm")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Input: "{}"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	// Still RUNNING: observing it publishes nothing yet.
	if _, err = m.DescribeExecution(ctx, exec.ARN); err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}

	if got, _ := sfnMetric(t, cw, clk, arn, "ExecutionsSucceeded"); got != 0 {
		t.Fatalf("running execution published ExecutionsSucceeded = %v", got)
	}

	clk.Advance(10 * time.Minute)

	got, err := m.DescribeExecution(ctx, exec.ARN)
	if err != nil || got.Status != driver.ExecStatusSucceeded {
		t.Fatalf("DescribeExecution after settle = %+v, %v", got, err)
	}

	// Further observations through every read path must not double-count.
	if _, err = m.ListExecutions(ctx, arn, ""); err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	if _, err = m.GetExecutionHistory(ctx, exec.ARN, false); err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	if _, err = m.DescribeExecution(ctx, exec.ARN); err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}

	for name, want := range map[string]float64{"ExecutionsStarted": 1, "ExecutionsSucceeded": 1} {
		if v, _ := sfnMetric(t, cw, clk, arn, name); v != want {
			t.Errorf("%s = %v, want %v", name, v, want)
		}
	}

	res, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "AWS/States", MetricName: "ExecutionTime",
		Dimensions: map[string]string{"StateMachineArn": arn},
		StartTime:  got.StopDate, EndTime: got.StopDate.Add(time.Second),
		Period: 1, Stat: "SampleCount",
	})
	if err != nil || len(res.Values) != 1 || res.Values[0] != 1 {
		t.Fatalf("ExecutionTime at StopDate %v = %+v (err %v), want one sample", got.StopDate, res, err)
	}

	if v := res.Unit; v != "Milliseconds" {
		t.Errorf("ExecutionTime unit = %q", v)
	}
}

// TestExecutionMetricsSettledListObservation pins that ListExecutions alone is
// enough to publish a settled run's close metrics.
func TestExecutionMetricsSettledListObservation(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m, cw := newMetricsMock(clk, config.WithAsyncSettle())
	ctx := context.Background()
	arn := createSM(t, m, "sm")

	if _, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Input: "{}"}); err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	clk.Advance(10 * time.Minute)

	if _, err := m.ListExecutions(ctx, arn, ""); err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	if v, _ := sfnMetric(t, cw, clk, arn, "ExecutionsSucceeded"); v != 1 {
		t.Errorf("ExecutionsSucceeded = %v, want 1", v)
	}
}
