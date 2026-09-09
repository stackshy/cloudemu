// Package driver defines the interface and types for the Amazon Managed Service
// for Prometheus (APS) control-plane API. It models APS workspaces plus their
// rule-groups namespaces, alert-manager definition, logging configuration and
// resource tagging.
//
// The emulator is control-plane only: it does not ingest, store or query
// Prometheus metrics. It models the fields clients and IaC tools read back — the
// computed workspace arn, workspaceId (ws-<uuid>), prometheusEndpoint, status
// and createdAt — minted once at create and returned unchanged on every later
// read so a Terraform plan never drifts. A workspace, a rule-groups namespace,
// an alert-manager definition and a logging configuration are each created
// directly in the ACTIVE state so IaC waiters that block on status do not hang.
// The rule-groups and alert-manager definition blobs are carried verbatim (as
// the base64 string the caller sent) so they round-trip byte-for-byte.
package driver

import (
	"context"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Workspace status codes. A newly created workspace is ACTIVE so Terraform's
// create waiter (which blocks until status is ACTIVE) completes without the
// real-cloud provisioning wait.
const (
	StatusCreating       = "CREATING"
	StatusActive         = "ACTIVE"
	StatusUpdating       = "UPDATING"
	StatusDeleting       = "DELETING"
	StatusCreationFailed = "CREATION_FAILED"
)

// Workspace is an APS workspace. Arn, WorkspaceID, PrometheusEndpoint, Status
// and CreatedAt are computed once at create and never regenerated, so repeated
// DescribeWorkspace/ListWorkspaces reads never drift. The child resources
// (RuleGroups, AlertManager, Logging) live under the workspace and are removed
// with it.
type Workspace struct {
	WorkspaceID        string
	Arn                string
	Alias              string
	Status             string
	KmsKeyArn          string
	PrometheusEndpoint string
	CreatedAt          time.Time
	Tags               map[string]string

	RuleGroups   map[string]RuleGroupsNamespace
	AlertManager *AlertManagerDefinition
	Logging      *LoggingConfiguration
}

// RuleGroupsNamespace is a rule-groups namespace under a workspace. Data holds
// the base64 string the caller sent, carried verbatim so it round-trips
// byte-for-byte.
type RuleGroupsNamespace struct {
	Name       string
	Arn        string
	Data       string
	Status     string
	CreatedAt  time.Time
	ModifiedAt time.Time
	Tags       map[string]string
}

// AlertManagerDefinition is the single alert-manager definition of a workspace.
// Data holds the base64 string the caller sent, carried verbatim.
type AlertManagerDefinition struct {
	Data       string
	Status     string
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// LoggingConfiguration is the single logging configuration of a workspace.
type LoggingConfiguration struct {
	LogGroupArn string
	Status      string
	CreatedAt   time.Time
	ModifiedAt  time.Time
}

// CreateWorkspaceInput is the input to CreateWorkspace.
type CreateWorkspaceInput struct {
	Alias     string
	KmsKeyArn string
	Tags      map[string]string
}

// RuleGroupsNamespaceInput is the input to CreateRuleGroupsNamespace and
// PutRuleGroupsNamespace. Data is the base64 string the caller sent.
type RuleGroupsNamespaceInput struct {
	WorkspaceID string
	Name        string
	Data        string
	Tags        map[string]string
}

// APS is the Amazon Managed Service for Prometheus control-plane surface:
// workspaces, rule-groups namespaces, the alert-manager definition, the logging
// configuration, and resource tags.
type APS interface {
	CreateWorkspace(ctx context.Context, in *CreateWorkspaceInput) (*Workspace, error)
	DescribeWorkspace(ctx context.Context, workspaceID string) (*Workspace, error)
	UpdateWorkspaceAlias(ctx context.Context, workspaceID, alias string) error
	DeleteWorkspace(ctx context.Context, workspaceID string) error
	ListWorkspaces(ctx context.Context, alias string, page Page) (workspaces []Workspace, nextToken string, err error)

	CreateLoggingConfiguration(ctx context.Context, workspaceID, logGroupArn string) (*LoggingConfiguration, error)
	DescribeLoggingConfiguration(ctx context.Context, workspaceID string) (*LoggingConfiguration, error)
	UpdateLoggingConfiguration(ctx context.Context, workspaceID, logGroupArn string) (*LoggingConfiguration, error)
	DeleteLoggingConfiguration(ctx context.Context, workspaceID string) error

	CreateRuleGroupsNamespace(ctx context.Context, in *RuleGroupsNamespaceInput) (*RuleGroupsNamespace, error)
	PutRuleGroupsNamespace(ctx context.Context, in *RuleGroupsNamespaceInput) (*RuleGroupsNamespace, error)
	DescribeRuleGroupsNamespace(ctx context.Context, workspaceID, name string) (*RuleGroupsNamespace, error)
	DeleteRuleGroupsNamespace(ctx context.Context, workspaceID, name string) error
	ListRuleGroupsNamespaces(
		ctx context.Context, workspaceID, name string, page Page,
	) (namespaces []RuleGroupsNamespace, nextToken string, err error)

	CreateAlertManagerDefinition(ctx context.Context, workspaceID, data string) (*AlertManagerDefinition, error)
	PutAlertManagerDefinition(ctx context.Context, workspaceID, data string) (*AlertManagerDefinition, error)
	DescribeAlertManagerDefinition(ctx context.Context, workspaceID string) (*AlertManagerDefinition, error)
	DeleteAlertManagerDefinition(ctx context.Context, workspaceID string) error

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
