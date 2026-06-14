package driver

import "context"

// Studio resource lifecycle status values (shared by Domain, UserProfile,
// Space). Apps use the App* subset.
const (
	StudioPending      = "Pending"
	StudioInService    = "InService"
	StudioUpdating     = "Updating"
	StudioDeleting     = "Deleting"
	StudioFailed       = "Failed"
	StudioUpdateFailed = "Update_Failed"
	StudioDeleteFailed = "Delete_Failed"
)

// App status values.
const (
	AppPending   = "Pending"
	AppInService = "InService"
	AppDeleting  = "Deleting"
	AppDeleted   = "Deleted"
	AppFailed    = "Failed"
)

// DomainSpec describes a Studio domain to create.
type DomainSpec struct {
	DomainName       string
	AuthMode         string // SSO, IAM
	VPCID            string
	SubnetIDs        []string
	ExecutionRoleARN string
	Tags             []Tag
}

// Domain is a Studio domain.
type Domain struct {
	DomainID         string
	DomainName       string
	DomainARN        string
	AuthMode         string
	VPCID            string
	SubnetIDs        []string
	ExecutionRoleARN string
	URL              string
	Status           string
	CreationTime     string
	Tags             []Tag
}

// UserProfileSpec describes a Studio user profile to create.
type UserProfileSpec struct {
	DomainID         string
	UserProfileName  string
	ExecutionRoleARN string
	Tags             []Tag
}

// UserProfile is a Studio user profile, scoped to a domain.
type UserProfile struct {
	DomainID         string
	UserProfileName  string
	UserProfileARN   string
	ExecutionRoleARN string
	Status           string
	CreationTime     string
	Tags             []Tag
}

// SpaceSpec describes a Studio space to create.
type SpaceSpec struct {
	DomainID  string
	SpaceName string
	Tags      []Tag
}

// Space is a Studio space, scoped to a domain.
type Space struct {
	DomainID     string
	SpaceName    string
	SpaceARN     string
	Status       string
	CreationTime string
	Tags         []Tag
}

// AppSpec describes a Studio app to create.
type AppSpec struct {
	DomainID        string
	UserProfileName string
	SpaceName       string
	AppType         string // JupyterServer, KernelGateway, JupyterLab, CodeEditor, ...
	AppName         string
}

// App is a Studio app.
type App struct {
	DomainID        string
	UserProfileName string
	SpaceName       string
	AppType         string
	AppName         string
	AppARN          string
	Status          string
	CreationTime    string
}

// studioAPI covers SageMaker Studio control-plane resources.
type studioAPI interface {
	CreateDomain(ctx context.Context, cfg DomainSpec) (*Domain, error)
	DescribeDomain(ctx context.Context, domainID string) (*Domain, error)
	ListDomains(ctx context.Context) ([]Domain, error)
	DeleteDomain(ctx context.Context, domainID string) error

	CreateUserProfile(ctx context.Context, cfg UserProfileSpec) (*UserProfile, error)
	DescribeUserProfile(ctx context.Context, domainID, name string) (*UserProfile, error)
	ListUserProfiles(ctx context.Context, domainID string) ([]UserProfile, error)
	DeleteUserProfile(ctx context.Context, domainID, name string) error

	CreateSpace(ctx context.Context, cfg SpaceSpec) (*Space, error)
	DescribeSpace(ctx context.Context, domainID, name string) (*Space, error)
	ListSpaces(ctx context.Context, domainID string) ([]Space, error)
	DeleteSpace(ctx context.Context, domainID, name string) error

	CreateApp(ctx context.Context, cfg AppSpec) (*App, error)
	DescribeApp(ctx context.Context, in AppSpec) (*App, error)
	ListApps(ctx context.Context, domainID string) ([]App, error)
	DeleteApp(ctx context.Context, in AppSpec) error
}
