// Package bedrockagent provides a portable Bedrock Agent authoring API with
// cross-cutting concerns. It wraps a driver.BedrockAgent with recording,
// metrics, rate limiting, error injection, and latency simulation.
package bedrockagent

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

const service = "bedrockagent"

// BedrockAgent is the portable Bedrock Agent type wrapping a driver with
// cross-cutting concerns.
type BedrockAgent struct {
	driver   driver.BedrockAgent
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewBedrockAgent creates a new portable BedrockAgent wrapping the given driver.
func NewBedrockAgent(d driver.BedrockAgent, opts ...Option) *BedrockAgent {
	b := &BedrockAgent{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable BedrockAgent.
type Option func(*BedrockAgent)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(b *BedrockAgent) { b.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *BedrockAgent) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(b *BedrockAgent) { b.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(b *BedrockAgent) { b.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *BedrockAgent) { b.latency = d } }

func (b *BedrockAgent) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check(service, op); err != nil {
			b.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.limiter != nil {
		if err := b.limiter.Allow(); err != nil {
			b.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.latency > 0 {
		time.Sleep(b.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if b.metrics != nil {
		labels := map[string]string{"service": service, "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.rec(op, input, out, err, dur)

	return out, err
}

func (b *BedrockAgent) rec(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record(service, op, input, output, err, dur)
	}
}

// CreateAgent creates a new agent.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateAgent(ctx context.Context, cfg driver.AgentConfig) (*driver.Agent, error) {
	out, err := b.do(ctx, "CreateAgent", cfg, func() (any, error) { return b.driver.CreateAgent(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Agent), nil
}

// GetAgent retrieves an agent by ID.
func (b *BedrockAgent) GetAgent(ctx context.Context, agentID string) (*driver.Agent, error) {
	out, err := b.do(ctx, "GetAgent", agentID, func() (any, error) { return b.driver.GetAgent(ctx, agentID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Agent), nil
}

// ListAgents lists all agents.
func (b *BedrockAgent) ListAgents(ctx context.Context) ([]driver.Agent, error) {
	out, err := b.do(ctx, "ListAgents", nil, func() (any, error) { return b.driver.ListAgents(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Agent), nil
}

// UpdateAgent updates an agent's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateAgent(ctx context.Context, agentID string, cfg driver.AgentConfig) (*driver.Agent, error) {
	out, err := b.do(ctx, "UpdateAgent", agentID, func() (any, error) { return b.driver.UpdateAgent(ctx, agentID, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Agent), nil
}

// DeleteAgent deletes an agent and returns its terminal status.
func (b *BedrockAgent) DeleteAgent(ctx context.Context, agentID string) (string, error) {
	out, err := b.do(ctx, "DeleteAgent", agentID, func() (any, error) { return b.driver.DeleteAgent(ctx, agentID) })
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// PrepareAgent prepares an agent, transitioning it to PREPARED.
func (b *BedrockAgent) PrepareAgent(ctx context.Context, agentID string) (*driver.Agent, error) {
	out, err := b.do(ctx, "PrepareAgent", agentID, func() (any, error) { return b.driver.PrepareAgent(ctx, agentID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Agent), nil
}

// CreateAgentAlias creates an alias of an agent.
func (b *BedrockAgent) CreateAgentAlias(ctx context.Context, cfg driver.AgentAliasConfig) (*driver.AgentAlias, error) {
	out, err := b.do(ctx, "CreateAgentAlias", cfg, func() (any, error) { return b.driver.CreateAgentAlias(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.AgentAlias), nil
}

// CreateKnowledgeBase creates a knowledge base.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateKnowledgeBase(ctx context.Context, cfg driver.KnowledgeBaseConfig) (*driver.KnowledgeBase, error) {
	out, err := b.do(ctx, "CreateKnowledgeBase", cfg, func() (any, error) { return b.driver.CreateKnowledgeBase(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.KnowledgeBase), nil
}

// GetKnowledgeBase retrieves a knowledge base by ID.
func (b *BedrockAgent) GetKnowledgeBase(ctx context.Context, id string) (*driver.KnowledgeBase, error) {
	out, err := b.do(ctx, "GetKnowledgeBase", id, func() (any, error) { return b.driver.GetKnowledgeBase(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.KnowledgeBase), nil
}

// ListKnowledgeBases lists all knowledge bases.
func (b *BedrockAgent) ListKnowledgeBases(ctx context.Context) ([]driver.KnowledgeBase, error) {
	out, err := b.do(ctx, "ListKnowledgeBases", nil, func() (any, error) { return b.driver.ListKnowledgeBases(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.KnowledgeBase), nil
}

// UpdateKnowledgeBase updates a knowledge base's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateKnowledgeBase(ctx context.Context, id string, cfg driver.KnowledgeBaseConfig) (*driver.KnowledgeBase, error) {
	out, err := b.do(ctx, "UpdateKnowledgeBase", id, func() (any, error) { return b.driver.UpdateKnowledgeBase(ctx, id, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.KnowledgeBase), nil
}

// DeleteKnowledgeBase deletes a knowledge base and returns its terminal status.
func (b *BedrockAgent) DeleteKnowledgeBase(ctx context.Context, id string) (string, error) {
	out, err := b.do(ctx, "DeleteKnowledgeBase", id, func() (any, error) { return b.driver.DeleteKnowledgeBase(ctx, id) })
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// CreateDataSource creates a data source under a knowledge base.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateDataSource(ctx context.Context, cfg driver.DataSourceConfig) (*driver.DataSource, error) {
	out, err := b.do(ctx, "CreateDataSource", cfg, func() (any, error) { return b.driver.CreateDataSource(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.DataSource), nil
}

// GetDataSource retrieves a data source by knowledge-base and data-source ID.
func (b *BedrockAgent) GetDataSource(ctx context.Context, kbID, dsID string) (*driver.DataSource, error) {
	out, err := b.do(ctx, "GetDataSource", dsID, func() (any, error) { return b.driver.GetDataSource(ctx, kbID, dsID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.DataSource), nil
}

// ListDataSources lists all data sources under a knowledge base.
func (b *BedrockAgent) ListDataSources(ctx context.Context, kbID string) ([]driver.DataSource, error) {
	out, err := b.do(ctx, "ListDataSources", kbID, func() (any, error) { return b.driver.ListDataSources(ctx, kbID) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.DataSource), nil
}

// UpdateDataSource updates a data source's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateDataSource(ctx context.Context, cfg driver.DataSourceConfig, dsID string) (*driver.DataSource, error) {
	out, err := b.do(ctx, "UpdateDataSource", dsID, func() (any, error) { return b.driver.UpdateDataSource(ctx, cfg, dsID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.DataSource), nil
}

// DeleteDataSource deletes a data source and returns its terminal status.
func (b *BedrockAgent) DeleteDataSource(ctx context.Context, kbID, dsID string) (string, error) {
	out, err := b.do(ctx, "DeleteDataSource", dsID, func() (any, error) { return b.driver.DeleteDataSource(ctx, kbID, dsID) })
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// StartIngestionJob starts an ingestion job for a data source.
func (b *BedrockAgent) StartIngestionJob(ctx context.Context, kbID, dsID, description string) (*driver.IngestionJob, error) {
	out, err := b.do(ctx, "StartIngestionJob", dsID, func() (any, error) {
		return b.driver.StartIngestionJob(ctx, kbID, dsID, description)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.IngestionJob), nil
}

// CreateFlow creates a flow.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreateFlow(ctx context.Context, cfg driver.FlowConfig) (*driver.Flow, error) {
	out, err := b.do(ctx, "CreateFlow", cfg, func() (any, error) { return b.driver.CreateFlow(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Flow), nil
}

// GetFlow retrieves a flow by identifier.
func (b *BedrockAgent) GetFlow(ctx context.Context, id string) (*driver.Flow, error) {
	out, err := b.do(ctx, "GetFlow", id, func() (any, error) { return b.driver.GetFlow(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Flow), nil
}

// ListFlows lists all flows.
func (b *BedrockAgent) ListFlows(ctx context.Context) ([]driver.Flow, error) {
	out, err := b.do(ctx, "ListFlows", nil, func() (any, error) { return b.driver.ListFlows(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Flow), nil
}

// UpdateFlow updates a flow's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdateFlow(ctx context.Context, id string, cfg driver.FlowConfig) (*driver.Flow, error) {
	out, err := b.do(ctx, "UpdateFlow", id, func() (any, error) { return b.driver.UpdateFlow(ctx, id, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Flow), nil
}

// DeleteFlow deletes a flow and returns its identifier.
func (b *BedrockAgent) DeleteFlow(ctx context.Context, id string) (string, error) {
	out, err := b.do(ctx, "DeleteFlow", id, func() (any, error) { return b.driver.DeleteFlow(ctx, id) })
	if err != nil {
		return "", err
	}

	return out.(string), nil
}

// PrepareFlow prepares a flow, transitioning it to Prepared.
func (b *BedrockAgent) PrepareFlow(ctx context.Context, id string) (*driver.Flow, error) {
	out, err := b.do(ctx, "PrepareFlow", id, func() (any, error) { return b.driver.PrepareFlow(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Flow), nil
}

// CreatePrompt creates a prompt.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) CreatePrompt(ctx context.Context, cfg driver.PromptConfig) (*driver.Prompt, error) {
	out, err := b.do(ctx, "CreatePrompt", cfg, func() (any, error) { return b.driver.CreatePrompt(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Prompt), nil
}

// GetPrompt retrieves a prompt by identifier.
func (b *BedrockAgent) GetPrompt(ctx context.Context, id string) (*driver.Prompt, error) {
	out, err := b.do(ctx, "GetPrompt", id, func() (any, error) { return b.driver.GetPrompt(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Prompt), nil
}

// ListPrompts lists all prompts.
func (b *BedrockAgent) ListPrompts(ctx context.Context) ([]driver.Prompt, error) {
	out, err := b.do(ctx, "ListPrompts", nil, func() (any, error) { return b.driver.ListPrompts(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Prompt), nil
}

// UpdatePrompt updates a prompt's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *BedrockAgent) UpdatePrompt(ctx context.Context, id string, cfg driver.PromptConfig) (*driver.Prompt, error) {
	out, err := b.do(ctx, "UpdatePrompt", id, func() (any, error) { return b.driver.UpdatePrompt(ctx, id, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Prompt), nil
}

// DeletePrompt deletes a prompt and returns its identifier.
func (b *BedrockAgent) DeletePrompt(ctx context.Context, id string) (string, error) {
	out, err := b.do(ctx, "DeletePrompt", id, func() (any, error) { return b.driver.DeletePrompt(ctx, id) })
	if err != nil {
		return "", err
	}

	return out.(string), nil
}
