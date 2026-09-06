// Package driver defines the interface and types for the AWS AppFlow
// control-plane API. It models flows (managed data-transfer configurations
// between SaaS applications and AWS services) and connector profiles, plus
// resource tagging.
//
// The emulator models the fields clients and IaC tools read back (the computed
// flowArn, flowStatus, connectorProfileArn, and the create/update timestamps)
// and carries the rich configuration blocks it does not interpret
// (sourceFlowConfig, destinationFlowConfigList, tasks, triggerConfig,
// metadataCatalogConfig, and the connector-profile config/credentials) verbatim
// as map[string]json.RawMessage, so a round-tripped resource reflects
// everything the caller sent. Actually running a flow and transferring data are
// out of scope for this control-plane surface: StartFlow/StopFlow only move the
// flow's status, they do not move any data.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Flow status values. A newly created flow is Active.
const (
	FlowStatusActive     = "Active"
	FlowStatusSuspended  = "Suspended"
	FlowStatusDeprecated = "Deprecated"
	FlowStatusDraft      = "Draft"
	FlowStatusErrored    = "Errored"
)

// Trigger types accepted by a flow's triggerConfig.
const (
	TriggerOnDemand  = "OnDemand"
	TriggerScheduled = "Scheduled"
	TriggerEvent     = "Event"
)

// Flow is an AppFlow flow. flowArn, flowStatus, createdAt, createdBy, and the
// summary connector/trigger types are computed once at create and never
// regenerated, so repeated reads never drift. Extra carries the request's
// configuration blocks (sourceFlowConfig, destinationFlowConfigList, tasks,
// triggerConfig, metadataCatalogConfig) verbatim.
type Flow struct {
	FlowName                 string
	FlowArn                  string
	Description              string
	KmsArn                   string
	FlowStatus               string
	SourceConnectorType      string
	DestinationConnectorType string
	TriggerType              string
	CreatedAt                time.Time
	LastUpdatedAt            time.Time
	CreatedBy                string
	LastUpdatedBy            string
	Tags                     map[string]string
	Extra                    map[string]json.RawMessage
}

// ConnectorProfile is an AppFlow connector profile. connectorProfileArn and the
// timestamps are computed once at create. Extra carries the connectorProfile
// config and credentials blocks verbatim.
type ConnectorProfile struct {
	ConnectorProfileName string
	ConnectorProfileArn  string
	ConnectorType        string
	ConnectorLabel       string
	ConnectionMode       string
	CredentialsArn       string
	CreatedAt            time.Time
	LastUpdatedAt        time.Time
	Tags                 map[string]string
	Extra                map[string]json.RawMessage
}

// CreateFlowInput is the input to CreateFlow. Extra carries the configuration
// blocks the emulator round-trips verbatim.
type CreateFlowInput struct {
	FlowName    string
	Description string
	KmsArn      string
	Tags        map[string]string
	Extra       map[string]json.RawMessage
}

// UpdateFlowInput is the input to UpdateFlow. The flowName identifies the flow
// and is not itself mutable; tags and kmsArn are set only at create, so they
// are not carried here.
type UpdateFlowInput struct {
	FlowName    string
	Description string
	Extra       map[string]json.RawMessage
}

// CreateConnectorProfileInput is the input to CreateConnectorProfile.
type CreateConnectorProfileInput struct {
	ConnectorProfileName string
	ConnectorType        string
	ConnectorLabel       string
	ConnectionMode       string
	Extra                map[string]json.RawMessage
}

// UpdateConnectorProfileInput is the input to UpdateConnectorProfile. The name
// identifies the profile; connectorType and connectionMode are immutable.
type UpdateConnectorProfileInput struct {
	ConnectorProfileName string
	Extra                map[string]json.RawMessage
}

// AppFlow is the AWS AppFlow control-plane surface: flows, connector profiles,
// and resource tags.
type AppFlow interface {
	CreateFlow(ctx context.Context, in *CreateFlowInput) (*Flow, error)
	DescribeFlow(ctx context.Context, flowName string) (*Flow, error)
	UpdateFlow(ctx context.Context, in *UpdateFlowInput) (*Flow, error)
	DeleteFlow(ctx context.Context, flowName string, forceDelete bool) error
	ListFlows(ctx context.Context, page Page) (flows []Flow, nextToken string, err error)
	StartFlow(ctx context.Context, flowName string) (flow *Flow, executionID string, err error)
	StopFlow(ctx context.Context, flowName string) (*Flow, error)

	CreateConnectorProfile(ctx context.Context, in *CreateConnectorProfileInput) (*ConnectorProfile, error)
	UpdateConnectorProfile(ctx context.Context, in *UpdateConnectorProfileInput) (*ConnectorProfile, error)
	DeleteConnectorProfile(ctx context.Context, name string, forceDelete bool) error
	DescribeConnectorProfiles(ctx context.Context, names []string, connectorType string,
		page Page) (profiles []ConnectorProfile, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
