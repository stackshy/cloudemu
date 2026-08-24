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

	// AWS SQS extras (ignored by non-AWS providers).
	ReceiveMessageWaitTimeSeconds int
	ContentBasedDeduplication     bool
	RedrivePolicy                 string // raw JSON, echoed by GetQueueAttributes
	// RedriveAllowPolicy is the raw JSON DLQ redrive-permission document
	// (redrivePermission=allowAll|denyAll|byQueue + sourceQueueArns). It is
	// persisted and echoed by GetQueueAttributes.
	RedriveAllowPolicy string
	// VisibilityTimeoutSet reports that VisibilityTimeout was supplied explicitly,
	// so the AWS provider can distinguish an explicit 0 (kept as 0) from an
	// omitted value (defaulted to 30). The wire handler sets it from attribute
	// presence; the typed Go API leaves it false, where 0 means "use the default".
	VisibilityTimeoutSet bool
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

// MessageAttributeValue is a typed user message attribute (SQS MessageAttributes).
type MessageAttributeValue struct {
	DataType    string
	StringValue string
	BinaryValue []byte
}

// SendMessageInput configures a message send operation.
type SendMessageInput struct {
	QueueURL        string
	Body            string
	DelaySeconds    int
	GroupID         string // FIFO only
	DeduplicationID string // FIFO only
	Attributes      map[string]string
	// MessageAttributes are typed user attributes (AWS SQS). Non-AWS providers ignore them.
	MessageAttributes map[string]MessageAttributeValue
	// SystemAttributes are SQS message system attributes (AWS SQS; the only
	// supported key is AWSTraceHeader). Non-AWS providers ignore them.
	SystemAttributes map[string]MessageAttributeValue
}

// SendMessageOutput is the result of sending a message.
type SendMessageOutput struct {
	MessageID      string
	SequenceNumber string // FIFO only
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
	// MessageAttributes are typed user attributes (AWS SQS).
	MessageAttributes map[string]MessageAttributeValue
	// SystemAttributes are SQS system attributes (SentTimestamp, ApproximateReceiveCount, SenderId, ...).
	SystemAttributes map[string]string
	// SequenceNumber is set for FIFO messages.
	SequenceNumber string
	// ReceiveCount is the number of times this message has been received. Azure
	// Queue Storage surfaces it as DequeueCount; providers that don't track it
	// leave it zero.
	ReceiveCount int
}

// BatchSendEntry represents a single message in a batch send.
type BatchSendEntry struct {
	ID                string
	Body              string
	DelaySeconds      int
	GroupID           string
	DeduplicationID   string
	Attributes        map[string]string
	MessageAttributes map[string]MessageAttributeValue
	// SystemAttributes are SQS message system attributes (AWS SQS; the only
	// supported key is AWSTraceHeader).
	SystemAttributes map[string]MessageAttributeValue
}

// BatchSendResult is the result of a batch send.
type BatchSendResult struct {
	Successful []BatchSendResultEntry
	Failed     []BatchSendFailEntry
}

// BatchSendResultEntry is a successful batch entry.
type BatchSendResultEntry struct {
	ID             string
	MessageID      string
	SequenceNumber string // FIFO only
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
	RedriveAllowPolicy         string // JSON DLQ redrive-permission document

	ReceiveMessageWaitTimeSeconds int
	ApproximateDelayedCount       int
	Policy                        string
	KmsMasterKeyID                string
}

// MessageMoveTask describes an SQS dead-letter-queue redrive task, as reported
// by StartMessageMoveTask / ListMessageMoveTasks. It is an AWS-specific concept
// (not part of the portable MessageQueue interface); only the AWS provider
// populates it, and the SQS wire handler type-asserts for the optional
// interface that returns it.
type MessageMoveTask struct {
	TaskHandle                   string
	SourceARN                    string
	DestinationARN               string
	MaxNumberOfMessagesPerSecond int
	Status                       string // AWS SQS task status, e.g. RUNNING, COMPLETED, FAILED
	ApproxMessagesMoved          int64
	ApproxMessagesToMove         int64
	FailureReason                string
	StartedAt                    time.Time
}

// AzureQueueMessage is a message returned by the Azure-specific Peek surface. It
// is a non-destructive read: no pop receipt is issued and visibility is
// unchanged.
type AzureQueueMessage struct {
	MessageID    string
	Body         string
	ReceiveCount int
	InsertedAt   time.Time
	ExpiresAt    time.Time
}

// AzureUpdateMessageResult is the outcome of an Azure Update Message call: a
// fresh pop receipt and the time the message will next become visible.
type AzureUpdateMessageResult struct {
	PopReceipt      string
	TimeNextVisible time.Time
}

// AzureQueueMetadata carries the queue properties Get Queue Metadata reports.
type AzureQueueMetadata struct {
	ApproximateMessageCount int
	Metadata                map[string]string
}

// AzureQueueStorage is the Azure-specific Queue Storage data-plane surface,
// kept off the cross-cloud MessageQueue interface (Peek, message content/
// visibility Update, and queue metadata are Azure Queue Storage concepts a
// provider opts into). The wire handler reaches it by type assertion, mirroring
// the AWS-specific message-move surface on the SQS side.
type AzureQueueStorage interface {
	// DequeueMessages retrieves up to maxMessages visible messages, hiding them
	// for visibilityTimeout seconds and issuing a pop receipt per message. It
	// respects Azure Queue Storage's max of 32 messages per call (distinct from
	// Service Bus's receive cap).
	DequeueMessages(ctx context.Context, queueURL string, maxMessages, visibilityTimeout int) ([]Message, error)
	// PeekMessages returns up to maxMessages visible messages without altering
	// their visibility or issuing pop receipts.
	PeekMessages(ctx context.Context, queueURL string, maxMessages int) ([]AzureQueueMessage, error)
	// UpdateMessage updates a message's content (when body is non-nil) and its
	// visibility timeout, returning a new pop receipt. The supplied popReceipt
	// must match the message's current receipt.
	UpdateMessage(
		ctx context.Context, queueURL, messageID, popReceipt string, visibilityTimeout int, body *string,
	) (AzureUpdateMessageResult, error)
	// GetQueueMetadata reports the approximate message count and user metadata.
	GetQueueMetadata(ctx context.Context, queueURL string) (AzureQueueMetadata, error)
	// SetQueueMetadata replaces the queue's user metadata.
	SetQueueMetadata(ctx context.Context, queueURL string, metadata map[string]string) error
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
