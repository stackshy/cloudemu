package bedrockagentruntime

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/Bedrock/Agents metrics, units and dimensions, as documented in
// "Monitor Amazon Bedrock Agents using CloudWatch Metrics".
const (
	metricNamespace    = "AWS/Bedrock/Agents"
	unitCount          = "Count"
	unitMilliseconds   = "Milliseconds"
	dimOperation       = "Operation"
	dimModelID         = "ModelId"
	dimAgentAliasARN   = "AgentAliasArn"
	metricInvocations  = "InvocationCount"
	metricTotalTime    = "TotalTime"
	metricClientErrors = "InvocationClientErrors"
	metricServerErrors = "InvocationServerErrors"
	metricThrottles    = "InvocationThrottles"
	metricModelCalls   = "ModelInvocationCount"
	metricModelLatency = "ModelLatency"
	metricInputTokens  = "InputTokenCount"
	metricOutputTokens = "OutputTokenCount"
)

// recordInvocation publishes the invocation metrics of one InvokeAgent or
// InvokeInlineAgent call: InvocationCount and TotalTime on success, the
// matching error count on failure. cfg is nil when the agent was not resolved.
func (m *Mock) recordInvocation(operation string, cfg *agentConfig, err error, took time.Duration) {
	if m.monitoring == nil {
		return
	}

	name := metricInvocations

	switch {
	case err == nil:
	case errors.IsThrottled(err):
		name = metricThrottles
	case errors.GetCode(err) == errors.Internal || errors.GetCode(err) == errors.Unavailable:
		name = metricServerErrors
	default:
		name = metricClientErrors
	}

	m.put(operation, cfg, name, 1, unitCount)

	if err == nil {
		m.put(operation, cfg, metricTotalTime, float64(took.Milliseconds()), unitMilliseconds)
	}
}

// recordModelUsage publishes the model metrics of a turn that called the model.
func (m *Mock) recordModelUsage(cfg *agentConfig, tokens [2]int) {
	if m.monitoring == nil || tokens == [2]int{} {
		return
	}

	m.put(cfg.operation, cfg, metricModelCalls, 1, unitCount)
	m.put(cfg.operation, cfg, metricModelLatency, 0, unitMilliseconds)
	m.put(cfg.operation, cfg, metricInputTokens, float64(tokens[0]), unitCount)
	m.put(cfg.operation, cfg, metricOutputTokens, float64(tokens[1]), unitCount)
}

// put publishes a datum under every dimension set the metric has: Operation;
// Operation and ModelId; and Operation, AgentAliasArn and ModelId.
func (m *Mock) put(operation string, cfg *agentConfig, name string, value float64, unit string) {
	sets := []map[string]string{{dimOperation: operation}}

	if cfg != nil && cfg.foundationModel != "" {
		sets = append(sets, map[string]string{dimOperation: operation, dimModelID: cfg.foundationModel})

		if cfg.aliasARN != "" {
			sets = append(sets, map[string]string{
				dimOperation: operation, dimAgentAliasARN: cfg.aliasARN, dimModelID: cfg.foundationModel,
			})
		}
	}

	data := make([]mondriver.MetricDatum, 0, len(sets))
	for _, dims := range sets {
		data = append(data, mondriver.MetricDatum{
			Namespace: metricNamespace, MetricName: name, Value: value, Unit: unit,
			Dimensions: dims, Timestamp: m.opts.Clock.Now(),
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}
