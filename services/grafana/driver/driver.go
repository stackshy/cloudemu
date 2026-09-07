// Package driver defines the interface and types for the Amazon Managed Grafana
// control-plane API (restJson1). It models Grafana workspaces plus their
// configuration string, authentication summary and resource tags.
//
// This is a control-plane-only surface: the emulator never runs a Grafana
// server. A workspace is created directly in the ACTIVE state so an IaC waiter
// that blocks on status does not hang. The computed fields clients and IaC
// tools read back — the id (g-[0-9a-f]{10}), the derived arn, the endpoint, the
// grafanaVersion, the status and the created timestamp — are minted once at
// create and stored, so repeated DescribeWorkspace/ListWorkspaces reads and a
// later UpdateWorkspace never drift. The nested vpcConfiguration and
// networkAccessControl blocks are carried verbatim as json.RawMessage so a
// round-tripped workspace reflects exactly what the caller sent; the request
// body renames its scalar fields (workspaceName, workspaceDescription, …) to
// the bare names the WorkspaceDescription reports, so those are modeled
// explicitly rather than passed through.
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

// Workspace status values. A newly created workspace is ACTIVE so an IaC create
// waiter (which blocks until status is ACTIVE) completes without a multi-minute
// real-cloud provisioning wait.
const (
	StatusActive   = "ACTIVE"
	StatusCreating = "CREATING"
	StatusUpdating = "UPDATING"
	StatusDeleting = "DELETING"
)

// SAML configuration status values reported on a workspace's authentication
// summary. A workspace using only IAM Identity Center reports NOT_CONFIGURED.
const (
	SamlNotConfigured = "NOT_CONFIGURED"
	SamlConfigured    = "CONFIGURED"
)

// Authentication provider values.
const (
	AuthAWSSSO = "AWS_SSO"
	AuthSAML   = "SAML"
)

// Workspace is an Amazon Managed Grafana workspace. ID, Arn, Endpoint,
// GrafanaVersion, Status and Created are computed once at create and never
// regenerated, so repeated reads never drift; Modified is bumped on every
// mutation. VpcConfiguration and NetworkAccessControl carry the request body's
// nested blocks verbatim.
type Workspace struct {
	ID                       string
	Arn                      string
	Name                     string
	Description              string
	Status                   string
	Endpoint                 string
	GrafanaVersion           string
	Created                  time.Time
	Modified                 time.Time
	AccountAccessType        string
	PermissionType           string
	AuthenticationProviders  []string
	SamlConfigurationStatus  string
	DataSources              []string
	NotificationDestinations []string
	OrganizationalUnits      []string
	OrganizationRoleName     string
	WorkspaceRoleArn         string
	StackSetName             string
	Configuration            string
	VpcConfiguration         json.RawMessage
	NetworkAccessControl     json.RawMessage
	Tags                     map[string]string
}

// CreateWorkspaceInput is the input to CreateWorkspace. Its JSON tags match the
// CreateWorkspace request body exactly, so the server unmarshals the request
// into it directly; the workspace-prefixed fields (workspaceName, …) map to the
// bare names the WorkspaceDescription reports.
type CreateWorkspaceInput struct {
	AccountAccessType        string            `json:"accountAccessType"`
	AuthenticationProviders  []string          `json:"authenticationProviders"`
	ClientToken              string            `json:"clientToken"`
	Configuration            string            `json:"configuration"`
	GrafanaVersion           string            `json:"grafanaVersion"`
	NetworkAccessControl     json.RawMessage   `json:"networkAccessControl"`
	OrganizationRoleName     string            `json:"organizationRoleName"`
	PermissionType           string            `json:"permissionType"`
	StackSetName             string            `json:"stackSetName"`
	Tags                     map[string]string `json:"tags"`
	VpcConfiguration         json.RawMessage   `json:"vpcConfiguration"`
	DataSources              []string          `json:"workspaceDataSources"`
	Description              string            `json:"workspaceDescription"`
	Name                     string            `json:"workspaceName"`
	NotificationDestinations []string          `json:"workspaceNotificationDestinations"`
	OrganizationalUnits      []string          `json:"workspaceOrganizationalUnits"`
	RoleArn                  string            `json:"workspaceRoleArn"`
}

// UpdateWorkspaceInput is the input to UpdateWorkspace. Pointer and raw fields
// distinguish "present" from "absent" so an omitted optional parameter leaves
// the stored value unchanged, matching UpdateWorkspace's merge semantics. Its
// JSON tags match the request body, so the server unmarshals into it directly
// and then sets ID from the URI.
type UpdateWorkspaceInput struct {
	ID                               string          `json:"-"`
	AccountAccessType                *string         `json:"accountAccessType"`
	NetworkAccessControl             json.RawMessage `json:"networkAccessControl"`
	OrganizationRoleName             *string         `json:"organizationRoleName"`
	PermissionType                   *string         `json:"permissionType"`
	RemoveNetworkAccessConfiguration bool            `json:"removeNetworkAccessConfiguration"`
	RemoveVpcConfiguration           bool            `json:"removeVpcConfiguration"`
	StackSetName                     *string         `json:"stackSetName"`
	VpcConfiguration                 json.RawMessage `json:"vpcConfiguration"`
	DataSources                      *[]string       `json:"workspaceDataSources"`
	Description                      *string         `json:"workspaceDescription"`
	Name                             *string         `json:"workspaceName"`
	NotificationDestinations         *[]string       `json:"workspaceNotificationDestinations"`
	OrganizationalUnits              *[]string       `json:"workspaceOrganizationalUnits"`
	RoleArn                          *string         `json:"workspaceRoleArn"`
}

// UpdateConfigurationInput is the input to UpdateWorkspaceConfiguration. A nil
// GrafanaVersion keeps the workspace's current version.
type UpdateConfigurationInput struct {
	ID             string
	Configuration  string
	GrafanaVersion *string
}

// UpdateAuthenticationInput is the input to UpdateWorkspaceAuthentication.
// SamlConfigured records whether a SAML configuration was supplied, which the
// authentication summary reports as its samlConfigurationStatus.
type UpdateAuthenticationInput struct {
	ID                      string
	AuthenticationProviders []string
	SamlConfigured          bool
}

// Grafana is the Amazon Managed Grafana control-plane surface: workspaces, their
// configuration string, their authentication summary, and resource tags.
type Grafana interface {
	CreateWorkspace(ctx context.Context, in *CreateWorkspaceInput) (*Workspace, error)
	DescribeWorkspace(ctx context.Context, id string) (*Workspace, error)
	UpdateWorkspace(ctx context.Context, in *UpdateWorkspaceInput) (*Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) (*Workspace, error)
	ListWorkspaces(ctx context.Context, page Page) (workspaces []Workspace, nextToken string, err error)

	UpdateWorkspaceConfiguration(ctx context.Context, in *UpdateConfigurationInput) error
	DescribeWorkspaceConfiguration(ctx context.Context, id string) (configuration, grafanaVersion string, err error)

	UpdateWorkspaceAuthentication(ctx context.Context, in *UpdateAuthenticationInput) (*Workspace, error)
	DescribeWorkspaceAuthentication(ctx context.Context, id string) (*Workspace, error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
