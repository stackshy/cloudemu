package sfn

import (
	"context"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// AWS/States execution metrics, published on the StateMachineArn dimension:
// the Executions* counts are Count, ExecutionTime is Milliseconds.
const (
	metricsNamespace = "AWS/States"
	redrivenPrefix   = "Redriven"

	unitCount        = "Count"
	unitMilliseconds = "Milliseconds"
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/States
// execution metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// closedMetric maps a terminal execution status to its Executions* metric.
func closedMetric(status string) string {
	switch status {
	case driver.ExecStatusSucceeded:
		return "ExecutionsSucceeded"
	case driver.ExecStatusFailed:
		return "ExecutionsFailed"
	case driver.ExecStatusTimedOut:
		return "ExecutionsTimedOut"
	case driver.ExecStatusAborted:
		return "ExecutionsAborted"
	default:
		return ""
	}
}

func (m *Mock) publishExec(ctx context.Context, smARN string, at time.Time, data []mondriver.MetricDatum) {
	if m.monitoring == nil {
		return
	}

	for i := range data {
		data[i].Namespace = metricsNamespace
		data[i].Dimensions = map[string]string{"StateMachineArn": smARN}
		data[i].Timestamp = at
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

// emitExecutionStarted publishes ExecutionsStarted for a new execution.
func (m *Mock) emitExecutionStarted(ctx context.Context, smARN string, at time.Time) {
	m.publishExec(ctx, smARN, at, []mondriver.MetricDatum{
		{MetricName: "ExecutionsStarted", Value: 1, Unit: unitCount},
	})
}

// executionClosed is the single completion hook of an execution: it runs
// exactly once per close (start-closed, first settled observation, abort, or
// redrive), outside every lock. It publishes the AWS/States close metrics; the
// execution status-change event belongs here as well.
func (m *Mock) executionClosed(ctx context.Context, exec *driver.Execution, prefix string) {
	m.emitExecutionClosed(ctx, exec, prefix)
}

// emitExecutionClosed publishes the close metrics of a terminal execution: its
// Executions<Status> count and ExecutionTime (start to stop). A redriven close
// (prefix "Redriven") also publishes ExecutionsRedriven and the matching
// Redriven<Executions...> count, as real Step Functions does.
func (m *Mock) emitExecutionClosed(ctx context.Context, exec *driver.Execution, prefix string) {
	name := closedMetric(exec.Status)
	if name == "" {
		return
	}

	data := []mondriver.MetricDatum{
		{MetricName: name, Value: 1, Unit: unitCount},
		{
			MetricName: "ExecutionTime", Unit: unitMilliseconds,
			Value: float64(exec.StopDate.Sub(exec.StartDate)) / float64(time.Millisecond),
		},
	}

	if prefix == redrivenPrefix {
		data = append(data,
			mondriver.MetricDatum{MetricName: "ExecutionsRedriven", Value: 1, Unit: unitCount},
			mondriver.MetricDatum{MetricName: redrivenPrefix + name, Value: 1, Unit: unitCount},
		)
	}

	m.publishExec(ctx, exec.StateMachineArn, exec.StopDate, data)
}
