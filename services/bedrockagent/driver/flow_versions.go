package driver

import "context"

// Flow alias concurrency types (ConcurrencyType).
const (
	FlowConcurrencyAutomatic = "Automatic"
	FlowConcurrencyManual    = "Manual"
)

// FlowAliasRoute is the routingConfiguration entry of a flow alias.
type FlowAliasRoute struct {
	FlowVersion string
}

// FlowAliasConcurrency is a flow alias's concurrencyConfiguration. A nil
// MaxConcurrency means none was given.
type FlowAliasConcurrency struct {
	Type           string
	MaxConcurrency *int32
}

// FlowAliasConfig describes a flow alias to create or update.
type FlowAliasConfig struct {
	FlowID               string
	Name                 string
	Description          string
	ClientToken          string
	RoutingConfiguration []FlowAliasRoute
	Concurrency          *FlowAliasConcurrency
	Tags                 map[string]string
}

// FlowAlias is an alias that routes invocations to a version of a flow.
type FlowAlias struct {
	ID                   string
	ARN                  string
	FlowID               string
	Name                 string
	Description          string
	RoutingConfiguration []FlowAliasRoute
	Concurrency          *FlowAliasConcurrency
	CreatedAt            string
	UpdatedAt            string
}

// FlowVersions publishes flow versions and manages the aliases routing to them.
//
// Versions are immutable numbered snapshots of DRAFT. Identifiers may be ids or
// ARNs.
// A version an alias routes to is deleted only with skipResourceInUseCheck.
type FlowVersions interface {
	CreateFlowVersion(ctx context.Context, flowID, description string) (*Flow, error)
	GetFlowVersion(ctx context.Context, flowID, version string) (*Flow, error)
	ListFlowVersions(ctx context.Context, flowID string, page Page) ([]Flow, string, error)
	DeleteFlowVersion(ctx context.Context, flowID, version string, skipResourceInUseCheck bool) error
	CreateFlowAlias(ctx context.Context, cfg FlowAliasConfig) (*FlowAlias, error)
	GetFlowAlias(ctx context.Context, flowID, aliasID string) (*FlowAlias, error)
	ListFlowAliases(ctx context.Context, flowID string, page Page) ([]FlowAlias, string, error)
	UpdateFlowAlias(ctx context.Context, aliasID string, cfg FlowAliasConfig) (*FlowAlias, error)
	DeleteFlowAlias(ctx context.Context, flowID, aliasID string) (*FlowAlias, error)
}
