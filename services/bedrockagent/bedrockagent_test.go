package bedrockagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	provider "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagent"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func newService() *BedrockAgent {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewBedrockAgent(provider.New(opts))
}

func TestServiceAgentLifecycle(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	agent, err := svc.CreateAgent(ctx, driver.AgentConfig{Name: "svc-agent", FoundationModel: "anthropic.claude-3-sonnet-20240229-v1:0"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if agent.ID == "" {
		t.Fatal("expected an agent id")
	}

	got, err := svc.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}

	if got.Name != "svc-agent" {
		t.Fatalf("got name %q, want svc-agent", got.Name)
	}

	prepared, err := svc.PrepareAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("PrepareAgent: %v", err)
	}

	if prepared.Status != driver.AgentPrepared {
		t.Fatalf("got status %q, want %q", prepared.Status, driver.AgentPrepared)
	}

	agents, _, err := svc.ListAgents(ctx, driver.Page{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("got %d agents, want 1", len(agents))
	}

	if _, err := svc.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	if _, err := svc.GetAgent(ctx, agent.ID); err == nil {
		t.Fatal("expected GetAgent to fail after delete")
	}
}

func TestServiceKnowledgeBaseLifecycle(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	kb, err := svc.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name:                       "svc-kb",
		RoleArn:                    "arn:aws:iam::123456789012:role/r",
		KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration:       []byte(`{"type":"OPENSEARCH_SERVERLESS"}`),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}

	got, err := svc.GetKnowledgeBase(ctx, kb.ID)
	if err != nil {
		t.Fatalf("GetKnowledgeBase: %v", err)
	}

	if got.Name != "svc-kb" {
		t.Fatalf("got name %q, want svc-kb", got.Name)
	}

	list, _, err := svc.ListKnowledgeBases(ctx, driver.Page{})
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("got %d knowledge bases, want 1", len(list))
	}
}

func TestServiceTagging(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	prompt, err := svc.CreatePrompt(ctx, driver.PromptConfig{Name: "p", Tags: map[string]string{"a": "1"}})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if err = svc.TagResource(ctx, prompt.ARN, map[string]string{"b": "2"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if err = svc.UntagResource(ctx, prompt.ARN, []string{"a"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err := svc.ListTagsForResource(ctx, prompt.ARN)
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags) != 1 || tags["b"] != "2" {
		t.Fatalf("tags = %v, want map[b:2]", tags)
	}

	two := int32(2)

	for range 3 {
		if _, err = svc.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r"}); err != nil {
			t.Fatalf("CreateFlow: %v", err)
		}
	}

	flows, next, err := svc.ListFlows(ctx, driver.Page{MaxResults: &two})
	if err != nil || len(flows) != 2 || next == "" {
		t.Fatalf("ListFlows = %d items, next %q, err %v", len(flows), next, err)
	}
}

// baseCalls calls every operation of the main BedrockAgent interface once on
// resources created up front, returning each call's error by operation.
func baseCalls(ctx context.Context, svc *BedrockAgent, ids map[string]string) map[string]error {
	kbCfg := driver.KnowledgeBaseConfig{
		Name: "kb", RoleArn: "arn:aws:iam::123456789012:role/r", KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration: []byte(`{"type":"OPENSEARCH_SERVERLESS"}`),
	}
	dsCfg := driver.DataSourceConfig{KnowledgeBaseID: ids["kb"], Name: "ds2", DataSourceConfiguration: []byte(`{"type":"S3"}`)}
	out := map[string]error{}

	_, out["GetAgent"] = svc.GetAgent(ctx, ids["agent"])
	_, _, out["ListAgents"] = svc.ListAgents(ctx, driver.Page{})
	_, out["UpdateAgent"] = svc.UpdateAgent(ctx, ids["agent"], driver.AgentConfig{
		Name: "a2", FoundationModel: "fm", ResourceRoleArn: "arn:aws:iam::123456789012:role/r",
	})
	_, out["PrepareAgent"] = svc.PrepareAgent(ctx, ids["agent"])
	_, out["CreateAgentAlias"] = svc.CreateAgentAlias(ctx, driver.AgentAliasConfig{AgentID: ids["agent"], Name: "live"})
	_, out["GetKnowledgeBase"] = svc.GetKnowledgeBase(ctx, ids["kb"])
	_, _, out["ListKnowledgeBases"] = svc.ListKnowledgeBases(ctx, driver.Page{})
	_, out["UpdateKnowledgeBase"] = svc.UpdateKnowledgeBase(ctx, ids["kb"], kbCfg)
	_, out["CreateDataSource"] = svc.CreateDataSource(ctx, dsCfg)
	_, out["GetDataSource"] = svc.GetDataSource(ctx, ids["kb"], ids["ds"])
	_, _, out["ListDataSources"] = svc.ListDataSources(ctx, ids["kb"], driver.Page{})
	_, out["UpdateDataSource"] = svc.UpdateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID: ids["kb"], Name: "ds3", DataSourceConfiguration: []byte(`{"type":"S3"}`),
	}, ids["ds"])
	_, out["StartIngestionJob"] = svc.StartIngestionJob(ctx, ids["kb"], ids["ds"], "")
	_, out["GetFlow"] = svc.GetFlow(ctx, ids["flow"])
	_, _, out["ListFlows"] = svc.ListFlows(ctx, driver.Page{})
	_, out["UpdateFlow"] = svc.UpdateFlow(ctx, ids["flow"], driver.FlowConfig{Name: "f2", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r"})
	_, out["PrepareFlow"] = svc.PrepareFlow(ctx, ids["flow"])
	_, out["GetPrompt"] = svc.GetPrompt(ctx, ids["prompt"])
	_, _, out["ListPrompts"] = svc.ListPrompts(ctx, driver.Page{})
	_, out["UpdatePrompt"] = svc.UpdatePrompt(ctx, ids["prompt"], driver.PromptConfig{Name: "p2"})
	_, out["DeleteDataSource"] = svc.DeleteDataSource(ctx, ids["kb"], ids["ds"])
	_, out["DeleteKnowledgeBase"] = svc.DeleteKnowledgeBase(ctx, ids["kb"])
	_, out["DeleteFlow"] = svc.DeleteFlow(ctx, ids["flow"])
	_, out["DeletePrompt"] = svc.DeletePrompt(ctx, ids["prompt"])
	_, out["DeleteAgent"] = svc.DeleteAgent(ctx, ids["agent"])

	return out
}

// seed creates one agent, knowledge base, data source, flow and prompt
// through a plain wrapper and returns their ids.
func seed(t *testing.T, svc *BedrockAgent) map[string]string {
	t.Helper()

	ctx := context.Background()

	agent, err := svc.CreateAgent(ctx, driver.AgentConfig{Name: "a", FoundationModel: "fm"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	kb, err := svc.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{
		Name: "kb", RoleArn: "arn:aws:iam::123456789012:role/r", KnowledgeBaseConfiguration: []byte(`{"type":"VECTOR"}`),
		StorageConfiguration: []byte(`{"type":"OPENSEARCH_SERVERLESS"}`),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}

	ds, err := svc.CreateDataSource(ctx, driver.DataSourceConfig{KnowledgeBaseID: kb.ID, Name: "ds", DataSourceConfiguration: []byte(`{"type":"S3"}`)})
	if err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	flow, err := svc.CreateFlow(ctx, driver.FlowConfig{Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/r"})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	prompt, err := svc.CreatePrompt(ctx, driver.PromptConfig{Name: "p"})
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	return map[string]string{"agent": agent.ID, "kb": kb.ID, "ds": ds.ID, "flow": flow.ID, "prompt": prompt.ID}
}

// TestServiceEveryBaseOperation runs every main-interface operation through
// the wrapper with a recorder, metrics and latency, then again with every
// operation failing by error injection, and once more past the rate limit.
func TestServiceEveryBaseOperation(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	drv := provider.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1")))
	rec := recorder.New()
	mc := metrics.NewCollector()
	svc := NewBedrockAgent(drv, WithRecorder(rec), WithMetrics(mc), WithLatency(time.Nanosecond))

	first := baseCalls(ctx, svc, seed(t, svc))

	for op, err := range first {
		if err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}

	if n := len(rec.CallsFor(service, "UpdateFlow")); n != 1 {
		t.Fatalf("recorded %d UpdateFlow calls, want 1", n)
	}

	if metrics.NewQuery(mc).ByName("calls_total").Count() == 0 {
		t.Fatal("no calls_total metric")
	}

	inj := inject.NewInjector()
	failing := NewBedrockAgent(drv, WithErrorInjection(inj))
	ids := seed(t, NewBedrockAgent(drv))

	for _, op := range []string{
		"CreateAgent", "CreateKnowledgeBase", "CreateFlow", "CreatePrompt", "ListTagsForResource", "TagResource", "UntagResource",
	} {
		inj.Set(service, op, errors.New("injected"), inject.Always{})
	}

	for op := range first {
		inj.Set(service, op, errors.New("injected"), inject.Always{})
	}

	for op, err := range baseCalls(ctx, failing, ids) {
		if err == nil {
			t.Errorf("%s: injected error not returned", op)
		}
	}

	creates := map[string]func() error{
		"CreateAgent": func() error { _, err := failing.CreateAgent(ctx, driver.AgentConfig{Name: "x"}); return err },
		"CreateKnowledgeBase": func() error {
			_, err := failing.CreateKnowledgeBase(ctx, driver.KnowledgeBaseConfig{Name: "x"})
			return err
		},
		"CreateFlow":          func() error { _, err := failing.CreateFlow(ctx, driver.FlowConfig{Name: "x"}); return err },
		"CreatePrompt":        func() error { _, err := failing.CreatePrompt(ctx, driver.PromptConfig{Name: "x"}); return err },
		"ListTagsForResource": func() error { _, err := failing.ListTagsForResource(ctx, "arn"); return err },
		"TagResource":         func() error { return failing.TagResource(ctx, "arn", map[string]string{"k": "v"}) },
		"UntagResource":       func() error { return failing.UntagResource(ctx, "arn", []string{"k"}) },
	}

	for op, call := range creates {
		if call() == nil {
			t.Errorf("%s: injected error not returned", op)
		}
	}

	limited := NewBedrockAgent(drv, WithRateLimiter(ratelimit.New(1, 1, fc)))

	if _, _, err := limited.ListAgents(ctx, driver.Page{}); err != nil {
		t.Fatalf("first call within the limit: %v", err)
	}

	if _, _, err := limited.ListAgents(ctx, driver.Page{}); err == nil {
		t.Fatal("second call past the limit must fail")
	}
}
