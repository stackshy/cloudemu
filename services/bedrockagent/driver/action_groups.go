package driver

import (
	"context"
	"encoding/json"
)

// ActionGroup state values (ActionGroupState).
const (
	ActionGroupEnabled  = "ENABLED"
	ActionGroupDisabled = "DISABLED"
)

// ActionGroupConfig describes an action group to create or update. Executor,
// APISchema and FunctionSchema are the actionGroupExecutor, apiSchema and
// functionSchema unions, kept verbatim.
type ActionGroupConfig struct {
	AgentID                          string
	AgentVersion                     string
	Name                             string
	Description                      string
	ClientToken                      string
	ParentActionGroupSignature       string
	ParentActionGroupSignatureParams map[string]string
	State                            string
	Executor                         json.RawMessage
	APISchema                        json.RawMessage
	FunctionSchema                   json.RawMessage
}

// ActionGroup is an action group of an agent version.
type ActionGroup struct {
	ID                               string
	AgentID                          string
	AgentVersion                     string
	Name                             string
	Description                      string
	ClientToken                      string
	ParentActionGroupSignature       string
	ParentActionGroupSignatureParams map[string]string
	State                            string
	Executor                         json.RawMessage
	APISchema                        json.RawMessage
	FunctionSchema                   json.RawMessage
	CreatedAt                        string
	UpdatedAt                        string
}

// ActionGroups manages the action groups (tools) of an agent version.
//
// Writes are allowed only on DRAFT; reads work on any version.
//
//nolint:dupl // same CRUD shape as the other agent-version children, different resource.
type ActionGroups interface {
	CreateAgentActionGroup(ctx context.Context, cfg ActionGroupConfig) (*ActionGroup, error)
	GetAgentActionGroup(ctx context.Context, agentID, version, actionGroupID string) (*ActionGroup, error)
	UpdateAgentActionGroup(ctx context.Context, actionGroupID string, cfg ActionGroupConfig) (*ActionGroup, error)
	DeleteAgentActionGroup(ctx context.Context, agentID, version, actionGroupID string) error
	ListAgentActionGroups(ctx context.Context, agentID, version string, page Page) ([]ActionGroup, string, error)
}
