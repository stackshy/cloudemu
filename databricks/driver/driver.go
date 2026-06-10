// Package driver defines the interface for Databricks-style analytics
// workspace services: lifecycle management of managed workspaces.
package driver

import "context"

// Provisioning state values for a workspace.
const (
	StateSucceeded = "Succeeded"
	StateCreating  = "Creating"
	StateDeleting  = "Deleting"
	StateFailed    = "Failed"
)

// WorkspaceConfig describes a workspace to create.
type WorkspaceConfig struct {
	Name                   string
	ResourceGroup          string
	Location               string
	SKUName                string
	SKUTier                string
	ManagedResourceGroupID string
	Tags                   map[string]string
}

// Workspace describes a managed analytics workspace.
type Workspace struct {
	ID                     string
	Name                   string
	ResourceGroup          string
	Location               string
	SKUName                string
	SKUTier                string
	ManagedResourceGroupID string
	WorkspaceURL           string
	WorkspaceID            string
	ProvisioningState      string
	Tags                   map[string]string
	CreatedAt              string
}

// Databricks is the interface that workspace service implementations must
// satisfy.
type Databricks interface {
	CreateWorkspace(ctx context.Context, cfg WorkspaceConfig) (*Workspace, error)
	GetWorkspace(ctx context.Context, resourceGroup, name string) (*Workspace, error)
	DeleteWorkspace(ctx context.Context, resourceGroup, name string) error
	UpdateWorkspaceTags(ctx context.Context, resourceGroup, name string, tags map[string]string) (*Workspace, error)
	ListWorkspacesByResourceGroup(ctx context.Context, resourceGroup string) ([]Workspace, error)
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
}
