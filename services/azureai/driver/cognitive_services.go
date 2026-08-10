package driver

import "context"

// AccountConfig describes a Cognitive Services account (the "Azure AI resource"
// / AI Foundry / Azure OpenAI account) to create or update.
type AccountConfig struct {
	Name          string
	ResourceGroup string
	Location      string
	Kind          string // AIServices | OpenAI | CognitiveServices | ...
	SKUName       string // S0, F0, ...
	CustomDomain  string
	Tags          map[string]string
}

// Account is a Microsoft.CognitiveServices/accounts resource.
type Account struct {
	ID                string
	Name              string
	ResourceGroup     string
	Location          string
	Kind              string
	SKUName           string
	Endpoint          string
	CustomDomain      string
	ProvisioningState string
	Tags              map[string]string
	CreatedAt         string
}

// DeploymentConfig describes a model deployment under an account.
type DeploymentConfig struct {
	Account       string
	ResourceGroup string
	Name          string
	ModelName     string
	ModelVersion  string
	ModelFormat   string // OpenAI | Microsoft | ...
	SKUName       string // Standard | GlobalStandard | ProvisionedManaged | ...
	SKUCapacity   int
}

// Deployment is a Microsoft.CognitiveServices/accounts/deployments resource.
type Deployment struct {
	ID                string
	Name              string
	ModelName         string
	ModelVersion      string
	ModelFormat       string
	SKUName           string
	SKUCapacity       int
	ProvisioningState string
	CreatedAt         string
}

// ProjectConfig describes an AI Foundry project under an account.
type ProjectConfig struct {
	Account       string
	ResourceGroup string
	Name          string
	Location      string
	DisplayName   string
	Description   string
	Tags          map[string]string
}

// Project is a Microsoft.CognitiveServices/accounts/projects resource (AI
// Foundry project).
type Project struct {
	ID                string
	Name              string
	Location          string
	DisplayName       string
	Description       string
	ProvisioningState string
	Tags              map[string]string
	CreatedAt         string
}

// RaiPolicyConfig describes a Responsible AI content-filter policy.
type RaiPolicyConfig struct {
	Account       string
	ResourceGroup string
	Name          string
	Mode          string // Default | Deferred | Blocking | Asynchronous_filter
	BasePolicy    string
}

// RaiPolicy is a Microsoft.CognitiveServices/accounts/raiPolicies resource.
type RaiPolicy struct {
	ID         string
	Name       string
	Mode       string
	BasePolicy string
	CreatedAt  string
}

// CommitmentPlanConfig describes a provisioned-throughput commitment plan.
type CommitmentPlanConfig struct {
	Account       string
	ResourceGroup string
	Name          string
	PlanType      string
	Tier          string
	AutoRenew     bool
}

// CommitmentPlan is a Microsoft.CognitiveServices/accounts/commitmentPlans
// resource.
type CommitmentPlan struct {
	ID                string
	Name              string
	PlanType          string
	Tier              string
	AutoRenew         bool
	ProvisioningState string
	CreatedAt         string
}

// PrivateEndpointConnection is a private-endpoint connection under an account.
type PrivateEndpointConnection struct {
	ID                string
	Name              string
	Status            string // Approved | Pending | Rejected
	Description       string
	ProvisioningState string
}

// AccountKeys holds the two access keys for an account.
type AccountKeys struct {
	Key1 string
	Key2 string
}

// AccountModel is an entry returned by the account's listModels surface.
type AccountModel struct {
	Name    string
	Version string
	Format  string
	Kind    string
}

// AccountSKU is an entry returned by the account's listSkus surface.
type AccountSKU struct {
	Name string
	Tier string
}

// Usage is a quota/usage datum returned by the account's listUsages surface.
type Usage struct {
	Name         string
	CurrentValue float64
	Limit        float64
	Unit         string
}

// CognitiveServices is the Microsoft.CognitiveServices control-plane surface.
type CognitiveServices interface {
	// Accounts.
	CreateAccount(ctx context.Context, cfg AccountConfig) (*Account, error)
	GetAccount(ctx context.Context, resourceGroup, name string) (*Account, error)
	DeleteAccount(ctx context.Context, resourceGroup, name string) error
	UpdateAccountTags(ctx context.Context, resourceGroup, name string, tags map[string]string) (*Account, error)
	ListAccountsByResourceGroup(ctx context.Context, resourceGroup string) ([]Account, error)
	ListAccounts(ctx context.Context) ([]Account, error)
	ListAccountKeys(ctx context.Context, resourceGroup, name string) (*AccountKeys, error)
	RegenerateAccountKey(ctx context.Context, resourceGroup, name, keyName string) (*AccountKeys, error)
	ListAccountUsages(ctx context.Context, resourceGroup, name string) ([]Usage, error)
	ListAccountModels(ctx context.Context, resourceGroup, name string) ([]AccountModel, error)
	ListAccountSkus(ctx context.Context, resourceGroup, name string) ([]AccountSKU, error)

	// Deployments (accounts/deployments).
	CreateDeployment(ctx context.Context, cfg DeploymentConfig) (*Deployment, error)
	GetDeployment(ctx context.Context, resourceGroup, account, name string) (*Deployment, error)
	DeleteDeployment(ctx context.Context, resourceGroup, account, name string) error
	ListDeployments(ctx context.Context, resourceGroup, account string) ([]Deployment, error)

	// Projects (accounts/projects).
	CreateProject(ctx context.Context, cfg ProjectConfig) (*Project, error)
	GetProject(ctx context.Context, resourceGroup, account, name string) (*Project, error)
	DeleteProject(ctx context.Context, resourceGroup, account, name string) error
	ListProjects(ctx context.Context, resourceGroup, account string) ([]Project, error)

	// RAI policies (accounts/raiPolicies).
	CreateRaiPolicy(ctx context.Context, cfg RaiPolicyConfig) (*RaiPolicy, error)
	GetRaiPolicy(ctx context.Context, resourceGroup, account, name string) (*RaiPolicy, error)
	DeleteRaiPolicy(ctx context.Context, resourceGroup, account, name string) error
	ListRaiPolicies(ctx context.Context, resourceGroup, account string) ([]RaiPolicy, error)

	// Commitment plans (accounts/commitmentPlans).
	CreateCommitmentPlan(ctx context.Context, cfg CommitmentPlanConfig) (*CommitmentPlan, error)
	GetCommitmentPlan(ctx context.Context, resourceGroup, account, name string) (*CommitmentPlan, error)
	DeleteCommitmentPlan(ctx context.Context, resourceGroup, account, name string) error
	ListCommitmentPlans(ctx context.Context, resourceGroup, account string) ([]CommitmentPlan, error)

	// Private-endpoint connections (accounts/privateEndpointConnections).
	PutPrivateEndpointConnection(ctx context.Context, resourceGroup, account, name, status string) (*PrivateEndpointConnection, error)
	GetPrivateEndpointConnection(ctx context.Context, resourceGroup, account, name string) (*PrivateEndpointConnection, error)
	DeletePrivateEndpointConnection(ctx context.Context, resourceGroup, account, name string) error
	ListPrivateEndpointConnections(ctx context.Context, resourceGroup, account string) ([]PrivateEndpointConnection, error)
}
