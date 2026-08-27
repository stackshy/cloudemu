// Package driver defines the interface for event bus service implementations.
package driver

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// EventBusInfo describes an event bus.
type EventBusInfo struct {
	Name      string
	ARN       string // provider-specific identifier
	State     string // "ACTIVE", "INACTIVE"
	CreatedAt string
	Tags      map[string]string
	Scope     scope.Scope
	// Region is the geographic location the resource was created in (Azure
	// location, GCP region). Empty for AWS and unscoped portable callers.
	Region string
	// InputSchema records the event schema a topic accepts (Azure Event
	// Grid: "EventGridSchema", "CustomEventSchema", "CloudEventSchemaV1_0").
	// Empty for AWS and GCP.
	InputSchema string
	// PublicNetworkAccess records whether the topic accepts traffic over the
	// public network (Azure Event Grid: "Enabled", "Disabled"). Empty for AWS
	// and GCP.
	PublicNetworkAccess string
	// Policy is the resource-based (IAM) policy document attached to the event
	// bus, as a JSON string. Managed by EventBridge PutPermission/RemovePermission
	// and surfaced on DescribeEventBus. Empty when the bus has no policy, and for
	// Azure and GCP.
	Policy string
}

// PermissionCondition is an optional condition on an EventBridge event-bus
// resource-policy statement, e.g. StringEquals on aws:PrincipalOrgID.
type PermissionCondition struct {
	Type  string // condition operator, e.g. "StringEquals"
	Key   string // condition key, e.g. "aws:PrincipalOrgID"
	Value string // condition value, e.g. "o-1234567890"
}

// PermissionInput carries the parameters of an EventBridge PutPermission call.
// It is either the legacy Action/Principal/StatementID trio (optionally with a
// Condition) or a full Policy JSON document — the two forms are mutually
// exclusive. Resource-based policies are an AWS-EventBridge concept, so this is
// consumed through an AWS-specific optional interface, not the portable driver.
type PermissionInput struct {
	StatementID string
	Action      string
	Principal   string
	Policy      string
	Condition   *PermissionCondition
}

// EventBusConfig configures a new event bus.
type EventBusConfig struct {
	Name string
	Tags map[string]string

	// Scope records where the resource lives (Azure subscription/resource
	// group, GCP project). Zero for AWS and unscoped portable callers.
	Scope scope.Scope
	// Region is the geographic location the resource is created in (Azure
	// location, GCP region). Empty for AWS and unscoped portable callers.
	Region string
	// InputSchema records the event schema a topic accepts (Azure Event
	// Grid: "EventGridSchema", "CustomEventSchema", "CloudEventSchemaV1_0").
	// Empty for AWS and GCP, and for an Azure caller accepting the default.
	InputSchema string
	// PublicNetworkAccess records whether the topic accepts traffic over the
	// public network (Azure Event Grid: "Enabled", "Disabled"). Empty for AWS
	// and GCP, and for an Azure caller accepting the default.
	PublicNetworkAccess string
}

// Rule defines an event routing rule with filtering.
type Rule struct {
	Name               string
	EventBus           string
	Description        string
	EventPattern       string // JSON pattern for event matching
	ScheduleExpression string // rate(...)/cron(...) for scheduled rules
	RoleARN            string // IAM role EventBridge assumes to invoke targets
	State              string // "ENABLED", "DISABLED"
	Targets            []Target
	CreatedAt          string
}

// RuleConfig configures a new rule.
type RuleConfig struct {
	Name               string
	EventBus           string
	Description        string
	EventPattern       string
	ScheduleExpression string
	RoleARN            string
	State              string
}

// Target is a destination for matched events. The structured fields
// (InputTransformer/DeadLetterConfig/RetryPolicy) carry raw JSON so they
// round-trip through the wire without the portable layer modeling every sub-shape.
type Target struct {
	ID               string
	ARN              string // target resource identifier
	Input            string // optional constant input
	InputPath        string // optional JSONPath input selector
	RoleARN          string // IAM role used to invoke this target
	InputTransformer string // raw JSON InputTransformer block
	DeadLetterConfig string // raw JSON DeadLetterConfig block
	RetryPolicy      string // raw JSON RetryPolicy block
}

// Event represents an event to publish.
type Event struct {
	ID         string
	Source     string
	DetailType string
	Detail     string // JSON string
	Time       time.Time
	EventBus   string
	Resources  []string
	// Subject is the event subject path (Azure Event Grid's resource-path
	// concept, e.g. "/blobServices/default/containers/x"). Empty for AWS
	// and GCP.
	Subject string
	// DataVersion is the publisher-supplied schema version of Detail (Azure
	// Event Grid's dataVersion). Preserved end-to-end so subscribers see the
	// version the publisher declared. Empty for AWS and GCP, and for an Azure
	// publisher that omitted it (delivery then defaults it to "1.0").
	DataVersion string
	// Topic overrides the delivered event's "topic" field (Azure Event Grid's
	// fully-qualified topic resource id). A system-topic producer (e.g. Blob
	// Storage) sets it to the source resource id — the storage account's ARM id —
	// which is the topic real Azure stamps on the event, distinct from the Event
	// Grid topic resource the subscription hangs off. Empty for AWS and GCP and
	// for a custom-topic publish, where delivery falls back to the bus's own ARN.
	Topic string
}

// PublishResult is the result of publishing events.
type PublishResult struct {
	SuccessCount int
	FailCount    int
	EventIDs     []string
}

// EventBus is the interface that event bus provider implementations must satisfy.
type EventBus interface {
	// Bus management
	CreateEventBus(ctx context.Context, config EventBusConfig) (*EventBusInfo, error)

	// UpdateEventBus replaces the mutable fields (tags) of an existing
	// event bus, mirroring ARM CreateOrUpdate-on-existing.
	UpdateEventBus(ctx context.Context, config EventBusConfig) (*EventBusInfo, error)
	DeleteEventBus(ctx context.Context, name string) error
	GetEventBus(ctx context.Context, name string) (*EventBusInfo, error)
	ListEventBuses(ctx context.Context, filter scope.Scope) ([]EventBusInfo, error)

	// Rule management
	PutRule(ctx context.Context, config *RuleConfig) (*Rule, error)
	DeleteRule(ctx context.Context, eventBus, ruleName string) error
	GetRule(ctx context.Context, eventBus, ruleName string) (*Rule, error)
	ListRules(ctx context.Context, eventBus string) ([]Rule, error)
	EnableRule(ctx context.Context, eventBus, ruleName string) error
	DisableRule(ctx context.Context, eventBus, ruleName string) error

	// Target management
	PutTargets(ctx context.Context, eventBus, ruleName string, targets []Target) error
	RemoveTargets(ctx context.Context, eventBus, ruleName string, targetIDs []string) error
	ListTargets(ctx context.Context, eventBus, ruleName string) ([]Target, error)

	// Event publishing
	PutEvents(ctx context.Context, events []Event) (*PublishResult, error)

	// Event history (replay)
	GetEventHistory(ctx context.Context, eventBus string, limit int) ([]Event, error)
}
