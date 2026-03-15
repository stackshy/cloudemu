// Package driver defines the interface for message queue service implementations.
package driver

import "context"

// QueueConfig describes a message queue to create.
type QueueConfig struct {
	Name                string
	FIFO                bool
	DelaySeconds        int
	VisibilityTimeout   int // seconds
	MaxMessageSize      int
	MessageRetention    int // seconds
	Tags                map[string]string
	DeadLetterQueue     *DeadLetterConfig
}

// DeadLetterConfig configures a dead-letter queue for failed messages.
type DeadLetterConfig struct {
	TargetQueueURL  string
	MaxReceiveCount int // move to DLQ after this many receives
}

// QueueInfo describes a message queue.
type QueueInfo struct {
	URL               string
	ARN               string
	Name              string
	FIFO              bool
	ApproxMessageCount int
	Tags              map[string]string
}

// SendMessageInput configures a message send operation.
type SendMessageInput struct {
	QueueURL       string
	Body           string
	DelaySeconds   int
	GroupID        string // FIFO only
	DeduplicationID string // FIFO only
	Attributes     map[string]string
}

// SendMessageOutput is the result of sending a message.
type SendMessageOutput struct {
	MessageID string
}

// ReceiveMessageInput configures a message receive operation.
type ReceiveMessageInput struct {
	QueueURL            string
	MaxMessages         int
	WaitTimeSeconds     int
	VisibilityTimeout   int
}

// Message is a received message.
type Message struct {
	MessageID     string
	ReceiptHandle string
	Body          string
	Attributes    map[string]string
	GroupID       string
}

// MessageQueue is the interface that message queue provider implementations must satisfy.
type MessageQueue interface {
	CreateQueue(ctx context.Context, config QueueConfig) (*QueueInfo, error)
	DeleteQueue(ctx context.Context, url string) error
	GetQueueInfo(ctx context.Context, url string) (*QueueInfo, error)
	ListQueues(ctx context.Context, prefix string) ([]QueueInfo, error)

	SendMessage(ctx context.Context, input SendMessageInput) (*SendMessageOutput, error)
	ReceiveMessages(ctx context.Context, input ReceiveMessageInput) ([]Message, error)
	DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error
	ChangeVisibility(ctx context.Context, queueURL, receiptHandle string, timeout int) error
}
