// Package driver defines the interface for message queue service implementations.
package driver

import (
	"context"
	"time"
)

// MaxBatchSize is the maximum number of entries allowed in a batch operation.
const MaxBatchSize = 10

// QueueConfig describes a message queue to create.
type QueueConfig struct {
	Name              string
	FIFO              bool
	DelaySeconds      int
	VisibilityTimeout int // seconds
	MaxMessageSize    int
	MessageRetention  int // seconds
	Tags              map[string]string
	DeadLetterQueue   *DeadLetterConfig
}

// DeadLetterConfig configures a dead-letter queue for failed messages.
type DeadLetterConfig struct {
	TargetQueueURL  string
	MaxReceiveCount int // move to DLQ after this many receives
}

// QueueInfo describes a message queue.
type QueueInfo struct {
	URL                string
	ARN                string
	Name               string
	FIFO               bool
	ApproxMessageCount int
	Tags               map[string]string
}

// SendMessageInput configures a message send operation.
type SendMessageInput struct {
	QueueURL        string
	Body            string
	DelaySeconds    int
	GroupID         string // FIFO only
	DeduplicationID string // FIFO only
	Attributes      map[string]string
}

// SendMessageOutput is the result of sending a message.
type SendMessageOutput struct {
	MessageID string
}

// ReceiveMessageInput configures a message receive operation.
type ReceiveMessageInput struct {
	QueueURL          string
	MaxMessages       int
	WaitTimeSeconds   int
	VisibilityTimeout int
}

// Message is a received message.
type Message struct {
	MessageID     string
	ReceiptHandle string
	Body          string
	Attributes    map[string]string
	GroupID       string
}

// BatchSendEntry represents a single message in a batch send.
type BatchSendEntry struct {
	ID              string
	Body            string
	DelaySeconds    int
	GroupID         string
	DeduplicationID string
	Attributes      map[string]string
}

// BatchSendResult is the result of a batch send.
type BatchSendResult struct {
	Successful []BatchSendResultEntry
	Failed     []BatchSendFailEntry
}

// BatchSendResultEntry is a successful batch entry.
type BatchSendResultEntry struct {
	ID        string
	MessageID string
}

// BatchSendFailEntry is a failed batch entry.
type BatchSendFailEntry struct {
	ID      string
	Code    string
	Message string
}

// BatchDeleteEntry represents a message to delete in batch.
type BatchDeleteEntry struct {
	ID            string
	ReceiptHandle string
}

// BatchDeleteResult is the result of a batch delete.
type BatchDeleteResult struct {
	Successful []string // entry IDs
	Failed     []BatchSendFailEntry
}

// ReceiveOptions configures a receive operation.
type ReceiveOptions struct {
	MaxMessages       int
	WaitTimeSeconds   int // long polling: 0 = short poll, >0 = check once
	VisibilityTimeout int // override queue default
}

// QueueAttributes describes queue attributes.
type QueueAttributes struct {
	DelaySeconds               int
	MaximumMessageSize         int
	MessageRetentionPeriod     int // seconds
	VisibilityTimeout          int // seconds
	ApproximateMessageCount    int
	ApproximateNotVisibleCount int
	CreatedAt                  time.Time
	LastModifiedAt             time.Time
	FifoQueue                  bool
	ContentBasedDeduplication  bool
	RedrivePolicy              string // JSON string pointing to DLQ
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

	// Batch operations
	SendMessageBatch(ctx context.Context, queue string, entries []BatchSendEntry) (*BatchSendResult, error)
	DeleteMessageBatch(ctx context.Context, queue string, entries []BatchDeleteEntry) (*BatchDeleteResult, error)

	// Enhanced receive with options
	ReceiveMessagesWithOptions(ctx context.Context, queue string, opts ReceiveOptions) ([]Message, error)

	// Queue attributes
	GetQueueAttributes(ctx context.Context, queue string) (*QueueAttributes, error)
	SetQueueAttributes(ctx context.Context, queue string, attrs map[string]int) error

	// Purge
	PurgeQueue(ctx context.Context, queue string) error
}
