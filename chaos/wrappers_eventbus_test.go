package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
)

func newChaosEventBus(t *testing.T) (ebdriver.EventBus, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapEventBus(cloudemu.NewAWS().EventBridge, e), e
}

func TestWrapEventBusCreateEventBusChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()

	if _, err := b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "ok"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if _, err := b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "fail"}); err == nil {
		t.Error("expected chaos error on CreateEventBus")
	}
}

func TestWrapEventBusDeleteEventBusChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if err := b.DeleteEventBus(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteEventBus")
	}
}

func TestWrapEventBusGetEventBusChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "g"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if _, err := b.GetEventBus(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetEventBus")
	}
}

func TestWrapEventBusListEventBusesChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if _, err := b.ListEventBuses(ctx); err == nil {
		t.Error("expected chaos error on ListEventBuses")
	}
}

func TestWrapEventBusPutRuleChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "r"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	cfg := &ebdriver.RuleConfig{Name: "rule", EventBus: "r", EventPattern: `{"source":["x"]}`, State: "ENABLED"}
	if _, err := b.PutRule(ctx, cfg); err == nil {
		t.Error("expected chaos error on PutRule")
	}
}

func TestWrapEventBusDeleteRuleChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "dr"})
	_, _ = b.PutRule(ctx, &ebdriver.RuleConfig{Name: "rule", EventBus: "dr", EventPattern: `{"source":["x"]}`, State: "ENABLED"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if err := b.DeleteRule(ctx, "dr", "rule"); err == nil {
		t.Error("expected chaos error on DeleteRule")
	}
}

func TestWrapEventBusGetRuleChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "gr"})
	_, _ = b.PutRule(ctx, &ebdriver.RuleConfig{Name: "rule", EventBus: "gr", EventPattern: `{"source":["x"]}`, State: "ENABLED"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if _, err := b.GetRule(ctx, "gr", "rule"); err == nil {
		t.Error("expected chaos error on GetRule")
	}
}

func TestWrapEventBusListRulesChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "lr"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	if _, err := b.ListRules(ctx, "lr"); err == nil {
		t.Error("expected chaos error on ListRules")
	}
}

func TestWrapEventBusPutEventsChaos(t *testing.T) {
	b, e := newChaosEventBus(t)
	ctx := context.Background()
	_, _ = b.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "pe"})

	e.Apply(chaos.ServiceOutage("eventbus", time.Hour))

	events := []ebdriver.Event{{Source: "x", DetailType: "y", Detail: "{}", EventBus: "pe", Time: time.Now()}}
	if _, err := b.PutEvents(ctx, events); err == nil {
		t.Error("expected chaos error on PutEvents")
	}
}
