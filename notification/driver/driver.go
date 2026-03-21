// Package driver defines the interface for notification service implementations.
package driver

import "context"

// TopicConfig describes a notification topic to create.
type TopicConfig struct {
	Name        string
	DisplayName string
	Tags        map[string]string
}

// TopicInfo describes a notification topic.
type TopicInfo struct {
	ID                string
	Name              string
	ResourceID        string
	DisplayName       string
	SubscriptionCount int
	Tags              map[string]string
}

// SubscriptionConfig describes a subscription to create.
type SubscriptionConfig struct {
	TopicID  string
	Protocol string // "email", "sms", "http", "https", "sqs", "lambda"
	Endpoint string
}

// SubscriptionInfo describes a subscription.
type SubscriptionInfo struct {
	ID       string
	TopicID  string
	Protocol string
	Endpoint string
	Status   string // "confirmed", "pending"
}

// PublishInput configures a message publish operation.
type PublishInput struct {
	TopicID    string
	Subject    string
	Message    string
	Attributes map[string]string
}

// PublishOutput is the result of publishing a message.
type PublishOutput struct {
	MessageID string
}

// Notification is the interface that notification provider implementations must satisfy.
type Notification interface {
	CreateTopic(ctx context.Context, config TopicConfig) (*TopicInfo, error)
	DeleteTopic(ctx context.Context, id string) error
	GetTopic(ctx context.Context, id string) (*TopicInfo, error)
	ListTopics(ctx context.Context) ([]TopicInfo, error)

	Subscribe(ctx context.Context, config SubscriptionConfig) (*SubscriptionInfo, error)
	Unsubscribe(ctx context.Context, subscriptionID string) error
	ListSubscriptions(ctx context.Context, topicID string) ([]SubscriptionInfo, error)

	Publish(ctx context.Context, input PublishInput) (*PublishOutput, error)
}
