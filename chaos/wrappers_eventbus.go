package chaos

import (
	"context"

	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
)

// chaosEventBus wraps an event bus driver. Hot-path: bus CRUD, rule CRUD,
// PutEvents. Enable/disable, target ops, and history delegate through.
type chaosEventBus struct {
	ebdriver.EventBus
	engine *Engine
}

// WrapEventBus returns an event bus driver that consults engine on bus, rule,
// and event publishing calls.
func WrapEventBus(inner ebdriver.EventBus, engine *Engine) ebdriver.EventBus {
	return &chaosEventBus{EventBus: inner, engine: engine}
}

func (c *chaosEventBus) CreateEventBus(
	ctx context.Context, cfg ebdriver.EventBusConfig,
) (*ebdriver.EventBusInfo, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "CreateEventBus"); err != nil {
		return nil, err
	}

	return c.EventBus.CreateEventBus(ctx, cfg)
}

func (c *chaosEventBus) DeleteEventBus(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "eventbus", "DeleteEventBus"); err != nil {
		return err
	}

	return c.EventBus.DeleteEventBus(ctx, name)
}

func (c *chaosEventBus) GetEventBus(ctx context.Context, name string) (*ebdriver.EventBusInfo, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "GetEventBus"); err != nil {
		return nil, err
	}

	return c.EventBus.GetEventBus(ctx, name)
}

func (c *chaosEventBus) ListEventBuses(ctx context.Context) ([]ebdriver.EventBusInfo, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "ListEventBuses"); err != nil {
		return nil, err
	}

	return c.EventBus.ListEventBuses(ctx)
}

func (c *chaosEventBus) PutRule(ctx context.Context, cfg *ebdriver.RuleConfig) (*ebdriver.Rule, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "PutRule"); err != nil {
		return nil, err
	}

	return c.EventBus.PutRule(ctx, cfg)
}

func (c *chaosEventBus) DeleteRule(ctx context.Context, eventBus, ruleName string) error {
	if err := applyChaos(ctx, c.engine, "eventbus", "DeleteRule"); err != nil {
		return err
	}

	return c.EventBus.DeleteRule(ctx, eventBus, ruleName)
}

func (c *chaosEventBus) GetRule(ctx context.Context, eventBus, ruleName string) (*ebdriver.Rule, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "GetRule"); err != nil {
		return nil, err
	}

	return c.EventBus.GetRule(ctx, eventBus, ruleName)
}

func (c *chaosEventBus) ListRules(ctx context.Context, eventBus string) ([]ebdriver.Rule, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "ListRules"); err != nil {
		return nil, err
	}

	return c.EventBus.ListRules(ctx, eventBus)
}

func (c *chaosEventBus) PutEvents(ctx context.Context, events []ebdriver.Event) (*ebdriver.PublishResult, error) {
	if err := applyChaos(ctx, c.engine, "eventbus", "PutEvents"); err != nil {
		return nil, err
	}

	return c.EventBus.PutEvents(ctx, events)
}
