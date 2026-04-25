package chaos

import (
	"context"

	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
)

// chaosMessageQueue wraps a message queue driver. Hot-path: queue CRUD plus
// send/receive/delete (single + batch). Visibility, attributes, and purge
// delegate through.
type chaosMessageQueue struct {
	mqdriver.MessageQueue
	engine *Engine
}

// WrapMessageQueue returns a message queue driver that consults engine on
// queue and message data-plane calls.
func WrapMessageQueue(inner mqdriver.MessageQueue, engine *Engine) mqdriver.MessageQueue {
	return &chaosMessageQueue{MessageQueue: inner, engine: engine}
}

func (c *chaosMessageQueue) CreateQueue(
	ctx context.Context, cfg mqdriver.QueueConfig,
) (*mqdriver.QueueInfo, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "CreateQueue"); err != nil {
		return nil, err
	}

	return c.MessageQueue.CreateQueue(ctx, cfg)
}

func (c *chaosMessageQueue) DeleteQueue(ctx context.Context, url string) error {
	if err := applyChaos(ctx, c.engine, "messagequeue", "DeleteQueue"); err != nil {
		return err
	}

	return c.MessageQueue.DeleteQueue(ctx, url)
}

func (c *chaosMessageQueue) ListQueues(ctx context.Context, prefix string) ([]mqdriver.QueueInfo, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "ListQueues"); err != nil {
		return nil, err
	}

	return c.MessageQueue.ListQueues(ctx, prefix)
}

//nolint:gocritic // input is a value type by interface contract
func (c *chaosMessageQueue) SendMessage(
	ctx context.Context, input mqdriver.SendMessageInput,
) (*mqdriver.SendMessageOutput, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "SendMessage"); err != nil {
		return nil, err
	}

	return c.MessageQueue.SendMessage(ctx, input)
}

func (c *chaosMessageQueue) ReceiveMessages(
	ctx context.Context, input mqdriver.ReceiveMessageInput,
) ([]mqdriver.Message, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "ReceiveMessages"); err != nil {
		return nil, err
	}

	return c.MessageQueue.ReceiveMessages(ctx, input)
}

func (c *chaosMessageQueue) DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error {
	if err := applyChaos(ctx, c.engine, "messagequeue", "DeleteMessage"); err != nil {
		return err
	}

	return c.MessageQueue.DeleteMessage(ctx, queueURL, receiptHandle)
}

func (c *chaosMessageQueue) SendMessageBatch(
	ctx context.Context, queue string, entries []mqdriver.BatchSendEntry,
) (*mqdriver.BatchSendResult, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "SendMessageBatch"); err != nil {
		return nil, err
	}

	return c.MessageQueue.SendMessageBatch(ctx, queue, entries)
}

func (c *chaosMessageQueue) DeleteMessageBatch(
	ctx context.Context, queue string, entries []mqdriver.BatchDeleteEntry,
) (*mqdriver.BatchDeleteResult, error) {
	if err := applyChaos(ctx, c.engine, "messagequeue", "DeleteMessageBatch"); err != nil {
		return nil, err
	}

	return c.MessageQueue.DeleteMessageBatch(ctx, queue, entries)
}
