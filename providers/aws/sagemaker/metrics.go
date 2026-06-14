package sagemaker

import (
	"context"

	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// emitMetric pushes a single metric to the wired monitoring backend (no-op
// when monitoring is not set), mirroring the S3/EC2 auto-metric pattern.
func (m *Mock) emitMetric(metricName string, value float64, unit string, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/SageMaker", MetricName: metricName, Value: value, Unit: unit,
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// emitJobMetric records the creation of a job of the given type.
func (m *Mock) emitJobMetric(jobType string) {
	m.emitMetric("JobsCreated", 1, "Count", map[string]string{"JobType": jobType})
}

// emitInvocation records an endpoint invocation.
func (m *Mock) emitInvocation(endpointName string) {
	m.emitMetric("Invocations", 1, "Count", map[string]string{"EndpointName": endpointName})
}
