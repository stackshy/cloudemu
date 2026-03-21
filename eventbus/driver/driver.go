// Package driver defines the interface for event bus service implementations.
package driver

import (
	"context"
	"time"
)

// EventBusInfo describes an event bus.
type EventBusInfo struct {
	Name      string
	ARN       string // provider-specific identifier
	State     string // "ACTIVE", "INACTIVE"
	CreatedAt string
	Tags      map[string]string
}

// EventBusConfig configures a new event bus.
type EventBusConfig struct {
	Name string
	Tags map[string]string
}

// Rule defines an event routing rule with filtering.
type Rule struct {
	Name         string
	EventBus     string
	Description  string
	EventPattern string // JSON pattern for event matching
	State        string // "ENABLED", "DISABLED"
	Targets      []Target
	CreatedAt    string
}

// RuleConfig configures a new rule.
type RuleConfig struct {
	Name         string
	EventBus     string
	Description  string
	EventPattern string
	State        string
}

// Target is a destination for matched events.
type Target struct {
	ID    string
	ARN   string // target resource identifier
	Input string // optional input transformation
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
	DeleteEventBus(ctx context.Context, name string) error
	GetEventBus(ctx context.Context, name string) (*EventBusInfo, error)
	ListEventBuses(ctx context.Context) ([]EventBusInfo, error)

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
