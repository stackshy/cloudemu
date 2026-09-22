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

const failDefinition = `{"StartAt":"F","States":{"F":{"Type":"Fail","Error":"Boom","Cause":"c"}}}`

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
		Name: "bad", Definition: failDefinition, RoleArn: "arn:aws:iam::000000000000:role/r",
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
