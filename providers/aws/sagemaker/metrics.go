package sagemaker

import (
	"context"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// nominalLatencyMs is the synthetic per-invocation ModelLatency the emulator
// reports (real endpoints publish a measured value).
const nominalLatencyMs = 42.0

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

// emitInvocation records an endpoint invocation plus a nominal ModelLatency,
// mirroring the two metrics real SageMaker endpoints publish most.
func (m *Mock) emitInvocation(endpointName string) {
	dims := map[string]string{"EndpointName": endpointName}
	m.emitMetric("Invocations", 1, "Count", dims)
	m.emitMetric("ModelLatency", nominalLatencyMs, "Milliseconds", dims)
}

// emitResourceCreated records the creation of a control-plane resource of the
// given type (endpoints, notebook instances, clusters, …).
func (m *Mock) emitResourceCreated(resourceType string) {
	m.emitMetric("ResourcesCreated", 1, "Count", map[string]string{"ResourceType": resourceType})
}
