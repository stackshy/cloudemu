package chaos

import (
	"context"

	notifdriver "github.com/stackshy/cloudemu/notification/driver"
)

// chaosNotification wraps a notification driver. All ops are wrapped — the
// surface is small and every call is data-plane.
type chaosNotification struct {
	notifdriver.Notification
	engine *Engine
}

// WrapNotification returns a notification driver that consults engine on
// every call.
func WrapNotification(inner notifdriver.Notification, engine *Engine) notifdriver.Notification {
	return &chaosNotification{Notification: inner, engine: engine}
}

func (c *chaosNotification) CreateTopic(
	ctx context.Context, cfg notifdriver.TopicConfig,
) (*notifdriver.TopicInfo, error) {
	if err := applyChaos(ctx, c.engine, "notification", "CreateTopic"); err != nil {
		return nil, err
	}

	return c.Notification.CreateTopic(ctx, cfg)
}

func (c *chaosNotification) DeleteTopic(ctx context.Context, id string) error {
	if err := applyChaos(ctx, c.engine, "notification", "DeleteTopic"); err != nil {
		return err
	}

	return c.Notification.DeleteTopic(ctx, id)
}

func (c *chaosNotification) GetTopic(ctx context.Context, id string) (*notifdriver.TopicInfo, error) {
	if err := applyChaos(ctx, c.engine, "notification", "GetTopic"); err != nil {
		return nil, err
	}

	return c.Notification.GetTopic(ctx, id)
}

func (c *chaosNotification) ListTopics(ctx context.Context) ([]notifdriver.TopicInfo, error) {
	if err := applyChaos(ctx, c.engine, "notification", "ListTopics"); err != nil {
		return nil, err
	}

	return c.Notification.ListTopics(ctx)
}

func (c *chaosNotification) Subscribe(
	ctx context.Context, cfg notifdriver.SubscriptionConfig,
) (*notifdriver.SubscriptionInfo, error) {
	if err := applyChaos(ctx, c.engine, "notification", "Subscribe"); err != nil {
		return nil, err
	}

	return c.Notification.Subscribe(ctx, cfg)
}

func (c *chaosNotification) Unsubscribe(ctx context.Context, subscriptionID string) error {
	if err := applyChaos(ctx, c.engine, "notification", "Unsubscribe"); err != nil {
		return err
	}

	return c.Notification.Unsubscribe(ctx, subscriptionID)
}

func (c *chaosNotification) Publish(
	ctx context.Context, input notifdriver.PublishInput,
) (*notifdriver.PublishOutput, error) {
	if err := applyChaos(ctx, c.engine, "notification", "Publish"); err != nil {
		return nil, err
	}

	return c.Notification.Publish(ctx, input)
}
