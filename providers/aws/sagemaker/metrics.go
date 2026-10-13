package sagemaker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// CloudWatch identity of SageMaker's native metrics. Endpoint invocation
// metrics live in AWS/SageMaker, per-job utilization in /aws/sagemaker/<Kind>Jobs
// (dimension Host), and pipeline execution metrics in the namespace AWS spells
// with a lowercase "m" (AWS/Sagemaker/ModelBuildingPipeline).
const (
	nsEndpoint = "AWS/SageMaker"
	nsPipeline = "AWS/Sagemaker/ModelBuildingPipeline"

	unitNone         = "None"
	unitMicroseconds = "Microseconds"
	unitPercent      = "Percent"

	// nominalModelLatencyUs / nominalOverheadLatencyUs are the synthetic
	// per-invocation latencies the emulator reports (real endpoints publish
	// measured values), in microseconds as real SageMaker does.
	nominalModelLatencyUs    = 42000.0
	nominalOverheadLatencyUs = 1000.0

	// nominalUtilizationPct is the synthetic utilization reported for a finished
	// job's host.
	nominalUtilizationPct = 50.0
)

// putMetrics pushes one batch to the wired monitoring backend (no-op when
// monitoring is not set).
func (m *Mock) putMetrics(ctx context.Context, data []mondriver.MetricDatum) {
	if m.monitoring == nil || len(data) == 0 {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

func (*Mock) datum(ns, name string, v float64, unit string, dims map[string]string, at time.Time) mondriver.MetricDatum {
	return mondriver.MetricDatum{Namespace: ns, MetricName: name, Value: v, Unit: unit, Dimensions: dims, Timestamp: at}
}

// variantDims are the dimensions every endpoint invocation metric carries.
func variantDims(endpoint, variant string) map[string]string {
	return map[string]string{"EndpointName": endpoint, "VariantName": variant}
}

// emitInvocation records a successful invocation: Invocations,
// InvocationsPerInstance, ModelLatency and OverheadLatency, in one batch.
//
// instances is the variant's serving instance count: AWS reports
// InvocationsPerInstance as 1/numberOfInstances per request, so a variant that
// serves nothing (scaled to zero) has no per-instance value.
func (m *Mock) emitInvocation(ctx context.Context, endpoint, variant string, instances int) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := variantDims(endpoint, variant)

	data := []mondriver.MetricDatum{
		m.datum(nsEndpoint, "Invocations", 1, unitNone, dims, now),
		m.datum(nsEndpoint, "ModelLatency", nominalModelLatencyUs, unitMicroseconds, dims, now),
		m.datum(nsEndpoint, "OverheadLatency", nominalOverheadLatencyUs, unitMicroseconds, dims, now),
	}

	if instances > 0 {
		data = append(data, m.datum(nsEndpoint, "InvocationsPerInstance", 1/float64(instances), unitNone, dims, now))
	}

	m.putMetrics(ctx, data)
}

// variantInstanceCount returns the serving instance count of a variant of ep.
func variantInstanceCount(ep *driver.Endpoint, variant string) int {
	for i := range ep.VariantSummaries {
		if ep.VariantSummaries[i].VariantName == variant {
			return ep.VariantSummaries[i].CurrentInstanceCount
		}
	}

	return 0
}

// emitInvocationFailure records a failed invocation of an existing endpoint:
// a 4XX or 5XX counter by status class, plus InvocationModelErrors when the
// model container itself failed the call.
func (m *Mock) emitInvocationFailure(ctx context.Context, endpoint, variant string, status int, modelError bool) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := variantDims(endpoint, variant)
	name := "Invocation4XXErrors"

	if status >= http.StatusInternalServerError {
		name = "Invocation5XXErrors"
	}

	data := []mondriver.MetricDatum{m.datum(nsEndpoint, name, 1, unitNone, dims, now)}
	if modelError {
		data = append(data, m.datum(nsEndpoint, "InvocationModelErrors", 1, unitNone, dims, now))
	}

	m.putMetrics(ctx, data)
}

// emitJobUtilization records the host utilization of a finished job under
// /aws/sagemaker/<kind>Jobs. Hosts are <job>/algo-<n> (training, processing)
// or <job>/<instance-id> (transform, which publishes no DiskUtilization);
// GPU metrics appear only on GPU instance types. Under --async-settle the
// metrics are skipped: the job has not finished at creation time.
func (m *Mock) emitJobUtilization(kind, job string, res driver.ResourceConfig, at string) {
	if m.monitoring == nil || m.opts.SettleDuration(settle.DefaultJobStageSettle) > 0 {
		return
	}

	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		ts = m.opts.Clock.Now()
	}

	count := res.InstanceCount
	if count < 1 {
		count = 1
	}

	if count > driver.MaxJobInstances {
		count = driver.MaxJobInstances
	}

	ns := "/aws/sagemaker/" + kind + "Jobs"
	gpu := isGPUInstance(res.InstanceType)

	var data []mondriver.MetricDatum

	for i := 1; i <= count; i++ {
		host := fmt.Sprintf("%s/algo-%d", job, i)
		if kind == "Transform" { //nolint:goconst // SDK enum literals
			host = fmt.Sprintf("%s/i-%08x", job, i)
		}

		dims := map[string]string{"Host": host}
		data = append(data,
			m.datum(ns, "CPUUtilization", nominalUtilizationPct, unitPercent, dims, ts),
			m.datum(ns, "MemoryUtilization", nominalUtilizationPct, unitPercent, dims, ts))

		if kind != "Transform" {
			data = append(data, m.datum(ns, "DiskUtilization", nominalUtilizationPct, unitPercent, dims, ts))
		}

		if gpu {
			data = append(data,
				m.datum(ns, "GPUUtilization", nominalUtilizationPct, unitPercent, dims, ts),
				m.datum(ns, "GPUMemoryUtilization", nominalUtilizationPct, unitPercent, dims, ts))
		}
	}

	m.putMetrics(context.Background(), data)
}

// isGPUInstance reports whether a SageMaker instance type carries GPUs (the p
// and g families, ml.<family>.<size>).
func isGPUInstance(instanceType string) bool {
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 || parts[0] != "ml" {
		return false
	}

	return strings.HasPrefix(parts[1], "p") || strings.HasPrefix(parts[1], "g")
}

// emitPipelineMetrics records a finished pipeline execution under
// AWS/Sagemaker/ModelBuildingPipeline.
func (m *Mock) emitPipelineMetrics(ctx context.Context, pipeline string, succeeded bool) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	dims := map[string]string{"PipelineName": pipeline}
	started, done := 1.0, 0.0

	failed := 0.0

	if succeeded {
		done = 1
	} else {
		failed = 1
	}

	m.putMetrics(ctx, []mondriver.MetricDatum{
		m.datum(nsPipeline, "ExecutionStarted", started, unitNone, dims, now),
		m.datum(nsPipeline, "ExecutionSucceeded", done, unitNone, dims, now),
		m.datum(nsPipeline, "ExecutionFailed", failed, unitNone, dims, now),
	})
}
