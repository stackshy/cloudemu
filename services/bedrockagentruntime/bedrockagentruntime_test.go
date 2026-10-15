package bedrockagentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	provider "github.com/stackshy/cloudemu/v2/providers/aws/bedrockagentruntime"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

func newService() *BedrockAgentRuntime {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewBedrockAgentRuntime(provider.New(opts))
}

func TestServiceInvokeAgent(t *testing.T) {
	svc := newService()

	out, err := svc.InvokeAgent(context.Background(), driver.InvokeAgentInput{
		AgentID:      "AGENT1",
		AgentAliasID: "ALIAS1",
		SessionID:    "s1",
		InputText:    "hi",
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}

	if !strings.Contains(out.Completion, "hi") || out.SessionID != "s1" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestServiceRetrieve(t *testing.T) {
	svc := newService()

	out, err := svc.Retrieve(context.Background(), driver.RetrieveInput{
		KnowledgeBaseID: "kb1",
		QueryText:       "q",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(out.Results) == 0 {
		t.Fatal("expected results")
	}
}

func TestServiceRetrieveAndGenerate(t *testing.T) {
	svc := newService()

	out, err := svc.RetrieveAndGenerate(context.Background(), driver.RetrieveAndGenerateInput{InputText: "q"})
	if err != nil {
		t.Fatalf("RetrieveAndGenerate: %v", err)
	}

	if out.Text == "" || out.SessionID == "" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// TestServiceCrossCuttingConcerns runs the three main operations through a
// recorder, metrics and latency, then with each failing by error injection,
// and once more past the rate limit.
func TestServiceCrossCuttingConcerns(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	drv := provider.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1")))
	rec := recorder.New()
	mc := metrics.NewCollector()
	svc := NewBedrockAgentRuntime(drv, WithRecorder(rec), WithMetrics(mc), WithLatency(time.Nanosecond))

	calls := func(b *BedrockAgentRuntime) map[string]error {
		out := map[string]error{}
		_, out["InvokeAgent"] = b.InvokeAgent(ctx, driver.InvokeAgentInput{AgentID: "A", AgentAliasID: "B", SessionID: "s1", InputText: "hi"})
		_, out["Retrieve"] = b.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: "kb1", QueryText: "q"})
		_, out["RetrieveAndGenerate"] = b.RetrieveAndGenerate(ctx, driver.RetrieveAndGenerateInput{InputText: "q"})

		return out
	}

	for op, err := range calls(svc) {
		if err != nil {
			t.Errorf("%s: %v", op, err)
		}

		if n := len(rec.CallsFor("bedrockagentruntime", op)); n != 1 {
			t.Errorf("%s recorded %d times, want 1", op, n)
		}
	}

	if metrics.NewQuery(mc).ByName("calls_total").Count() == 0 {
		t.Fatal("no calls_total metric")
	}

	inj := inject.NewInjector()
	for _, op := range []string{"InvokeAgent", "Retrieve", "RetrieveAndGenerate"} {
		inj.Set("bedrockagentruntime", op, errors.New("injected"), inject.Always{})
	}

	for op, err := range calls(NewBedrockAgentRuntime(drv, WithErrorInjection(inj), WithMetrics(mc), WithRecorder(rec))) {
		if err == nil {
			t.Errorf("%s: injected error not returned", op)
		}
	}

	limited := NewBedrockAgentRuntime(drv, WithRateLimiter(ratelimit.New(1, 1, fc)))

	if _, err := limited.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: "kb1", QueryText: "q"}); err != nil {
		t.Fatalf("first call within the limit: %v", err)
	}

	if _, err := limited.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: "kb1", QueryText: "q"}); err == nil {
		t.Fatal("second call past the limit must fail")
	}
}
