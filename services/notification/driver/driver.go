// Package driver defines the interface for notification service implementations.
package driver

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TopicConfig describes a notification topic to create.
type TopicConfig struct {
	Name        string
	DisplayName string
	Tags        map[string]string

	// Policy is the JSON access policy (AWS SNS). Empty leaves the topic on
	// its synthesized default policy.
	Policy string
	// DeliveryPolicy is the JSON HTTP delivery policy (AWS SNS). Empty leaves
	// the topic on its synthesized default.
	DeliveryPolicy string
	// KmsMasterKeyID names the KMS key for server-side encryption (AWS SNS).
	KmsMasterKeyID string
	// FifoTopic marks the topic as FIFO. ContentBasedDeduplication toggles
	// content-based deduplication on a FIFO topic. Both are persisted and echoed
	// by GetTopicAttributes; FIFO publish ordering/dedup is not yet enforced.
	FifoTopic                 bool
	ContentBasedDeduplication bool

	// Scope records where the resource lives (Azure subscription/resource
	// group, GCP project). Zero for AWS and unscoped portable callers.
	Scope scope.Scope
	// Region is the geographic location the resource is created in (Azure
	// location). Empty for AWS and unscoped portable callers.
	Region string
}

// TopicInfo describes a notification topic.
type TopicInfo struct {
	ID          string
	Name        string
	ResourceID  string
	DisplayName string
	// SubscriptionCount is the total number of subscriptions on the topic
	// (confirmed + pending). AWS SNS reports the breakdown separately.
	SubscriptionCount int
	// SubscriptionsConfirmed / SubscriptionsPending / SubscriptionsDeleted mirror
	// the SNS GetTopicAttributes counters. Deleted is a monotonic tally of
	// unsubscribed endpoints.
	SubscriptionsConfirmed int
	SubscriptionsPending   int
	SubscriptionsDeleted   int
	// Policy is the JSON access policy (AWS SNS); empty means the default.
	Policy string
	// DeliveryPolicy is the JSON HTTP delivery policy (AWS SNS); empty means the
	// default. KmsMasterKeyID names the server-side-encryption KMS key.
	DeliveryPolicy string
	KmsMasterKeyID string
	// FifoTopic / ContentBasedDeduplication reflect the FIFO flags captured at
	// create time. GetTopicAttributes echoes them; publish ordering is not
	// enforced.
	FifoTopic                 bool
	ContentBasedDeduplication bool
	Tags                      map[string]string
	Scope                     scope.Scope
	// Region is the geographic location the resource was created in (Azure
	// location). Empty for AWS and unscoped portable callers.
	Region string
}

// SubscriptionConfig describes a subscription to create.
type SubscriptionConfig struct {
	TopicID  string
	Protocol string // "email", "sms", "http", "https", "sqs", "lambda"
	Endpoint string
	// Attributes carries subscription attributes accepted at Subscribe time
	// (FilterPolicy, RawMessageDelivery, RedrivePolicy, DeliveryPolicy, ...).
	Attributes map[string]string
}

// SubscriptionInfo describes a subscription.
type SubscriptionInfo struct {
	ID       string
	TopicID  string
	Protocol string
	Endpoint string
	Status   string // "confirmed", "pending"
	// Attributes holds the subscription's mutable attributes (FilterPolicy,
	// RawMessageDelivery, RedrivePolicy, DeliveryPolicy, ...).
	Attributes map[string]string
	// ConfirmationToken is the opaque token an AWS SNS pending subscription is
	// confirmed with via ConfirmSubscription. Empty for auto-confirmed subs.
	ConfirmationToken string
}

// MessageAttribute is a typed SNS message attribute carried on a publish.
// DataType is "String", "Number", or "Binary". Value holds the string (or
// Number) value; for a Binary attribute Value holds the base64-encoded bytes.
type MessageAttribute struct {
	DataType string
	Value    string
}

// PublishInput configures a message publish operation.
type PublishInput struct {
	TopicID string
	Subject string
	Message string
	// Attributes carries the flat name->value view of the message attributes,
	// used for filter-policy matching and cross-cloud publishes. AttributeEntries
	// carries the typed (DataType-preserving) view used to build the SNS delivery
	// envelope; it is nil for non-SNS publishes.
	Attributes       map[string]string
	AttributeEntries map[string]MessageAttribute
	// MessageStructure is "json" when Message is a per-protocol JSON blob
	// ({"default":...,"sqs":...}); empty means Message is delivered verbatim.
	MessageStructure string
	// MessageGroupID / MessageDeduplicationID carry the FIFO ordering and
	// deduplication identifiers of a publish to a FIFO topic. They are empty for
	// standard topics and are threaded to a FIFO SQS subscription so the queue,
	// which requires a message group id, accepts the fan-out delivery.
	MessageGroupID         string
	MessageDeduplicationID string
}

// PublishOutput is the result of publishing a message.
type PublishOutput struct {
	MessageID string
}

// Notification is the interface that notification provider implementations must satisfy.
type Notification interface {
	CreateTopic(ctx context.Context, config TopicConfig) (*TopicInfo, error)

	// UpdateTopic replaces the mutable fields (display name, tags) of an
	// existing topic, mirroring ARM CreateOrUpdate-on-existing.
	UpdateTopic(ctx context.Context, config TopicConfig) (*TopicInfo, error)
	DeleteTopic(ctx context.Context, id string) error
	GetTopic(ctx context.Context, id string) (*TopicInfo, error)
	ListTopics(ctx context.Context, filter scope.Scope) ([]TopicInfo, error)

	Subscribe(ctx context.Context, config SubscriptionConfig) (*SubscriptionInfo, error)
	Unsubscribe(ctx context.Context, subscriptionID string) error
	ListSubscriptions(ctx context.Context, topicID string) ([]SubscriptionInfo, error)

	Publish(ctx context.Context, input PublishInput) (*PublishOutput, error)
}

// AzureNamespaceMeta carries Azure Notification Hubs namespace metadata that
// has no cross-cloud analog (SKU tier). Kept off the portable Notification
// interface; the Azure wire handler reaches it via the AzureNotificationHubs
// optional capability.
type AzureNamespaceMeta struct {
	SKU string // "Free" | "Basic" | "Standard"
}

// AzureSASRule is an Azure Shared Access authorization rule. The keys are
// generated by the provider and echoed back by ListKeys.
type AzureSASRule struct {
	Rights       []string // subset of "Listen", "Send", "Manage"
	PrimaryKey   string
	SecondaryKey string
}

// AzureRegistration is a Notification Hubs data-plane device registration. Body
// carries the raw registration description so the exact PNS payload round-trips
// without the driver modeling every platform's shape.
type AzureRegistration struct {
	RegistrationID string
	ETag           string
	Platform       string   // "gcm" | "fcm" | "apple" | "windows" | ...
	Handle         string   // PNS handle / device token
	Tags           []string // registration tags
	Body           string   // raw <RegistrationDescription> inner XML
}

// AzureNotificationHubs is the Azure-only Notification Hubs surface: namespace
// SKU, Shared Access authorization rules and data-plane device registrations.
// None have an AWS/GCP analog, so they live on this optional interface rather
// than the cross-cloud Notification interface. Resource keys are the driver
// topic keys (namespace name, or "{namespace}/{hub}"). A provider opts in by
// implementing it; the wire handler reaches it via type assertion.
type AzureNotificationHubs interface {
	SetNamespaceMeta(ctx context.Context, namespace string, meta AzureNamespaceMeta) error
	GetNamespaceMeta(ctx context.Context, namespace string) (AzureNamespaceMeta, error)

	PutSASRule(ctx context.Context, resourceKey, ruleName string, rule AzureSASRule) (AzureSASRule, error)
	GetSASRule(ctx context.Context, resourceKey, ruleName string) (AzureSASRule, error)
	ListSASRules(ctx context.Context, resourceKey string) (map[string]AzureSASRule, error)
	DeleteSASRule(ctx context.Context, resourceKey, ruleName string) error
	// RegenerateSASKey rotates the primary or secondary key of a rule (policyKey
	// is "PrimaryKey" or "SecondaryKey"), returning the rule with the new key.
	// The change is durable: later GetSASRule/ListKeys observe the rotated value.
	RegenerateSASKey(ctx context.Context, resourceKey, ruleName, policyKey string) (AzureSASRule, error)

	CreateRegistration(ctx context.Context, hubKey string, reg AzureRegistration) (AzureRegistration, error)
	GetRegistration(ctx context.Context, hubKey, registrationID string) (AzureRegistration, error)
	ListRegistrations(ctx context.Context, hubKey string) ([]AzureRegistration, error)
	DeleteRegistration(ctx context.Context, hubKey, registrationID string) error

	// SetPnsCredentials stores a hub's Platform Notification Service credentials
	// as the raw properties JSON supplied at hub create/update time; GetPnsCredentials
	// returns it (empty when none were set). Opaque like AzureRegistration.Body so
	// every platform's credential shape round-trips without the driver modeling it.
	SetPnsCredentials(ctx context.Context, hubKey, credentialsJSON string) error
	GetPnsCredentials(ctx context.Context, hubKey string) (string, error)
}
