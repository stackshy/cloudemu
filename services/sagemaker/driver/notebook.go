package driver

import "context"

// Notebook instance status values.
const (
	NotebookPending   = "Pending"
	NotebookInService = "InService"
	NotebookStopping  = "Stopping"
	NotebookStopped   = "Stopped"
	NotebookDeleting  = "Deleting"
	NotebookUpdating  = "Updating"
	NotebookFailed    = "Failed"
)

// NotebookInstanceSpec describes a notebook instance to create.
type NotebookInstanceSpec struct {
	Name            string
	InstanceType    string
	RoleARN         string
	VolumeSizeInGB  int
	LifecycleConfig string
	DefaultCodeRepo string
	// AdditionalCodeRepos lists up to 3 further repositories.
	AdditionalCodeRepos []string
	RootAccess          string // Enabled (default) or Disabled
	// Network and encryption inputs, echoed by Describe.
	SubnetID             string
	SecurityGroups       []string
	KmsKeyID             string
	DirectInternetAccess string // Enabled (default) or Disabled
	PlatformIdentifier   string
	Tags                 []Tag
}

// Notebook limits and defaults.
const (
	MinNotebookVolumeGB     = 5
	MaxNotebookVolumeGB     = 16384
	DefaultNotebookVolumeGB = 5
	MaxAdditionalCodeRepos  = 3
	NotebookRootAccessOn    = "Enabled"
	NotebookRootAccessOff   = "Disabled"
)

// NotebookInstance is a managed notebook instance.
type NotebookInstance struct {
	Name            string
	ARN             string
	InstanceType    string
	RoleARN         string
	VolumeSizeInGB  int
	LifecycleConfig string
	DefaultCodeRepo string
	// AdditionalCodeRepos lists up to 3 further repositories.
	AdditionalCodeRepos  []string
	RootAccess           string
	SubnetID             string
	SecurityGroups       []string
	KmsKeyID             string
	DirectInternetAccess string
	PlatformIdentifier   string
	Status               string
	URL                  string
	FailureReason        string
	CreationTime         string
	LastModifiedTime     string
	Tags                 []Tag
}

// NotebookLifecycleConfigSpec describes a lifecycle config to create.
type NotebookLifecycleConfigSpec struct {
	Name     string
	OnCreate string
	OnStart  string
}

// NotebookLifecycleConfig is a notebook lifecycle configuration.
type NotebookLifecycleConfig struct {
	Name             string
	ARN              string
	OnCreate         string
	OnStart          string
	CreationTime     string
	LastModifiedTime string
}

// NotebookInstanceUpdate describes an UpdateNotebookInstance request. A nil
// pointer leaves the setting unchanged; the Disassociate flags clear it.
type NotebookInstanceUpdate struct {
	Name                            string
	InstanceType                    *string
	RoleARN                         *string
	VolumeSizeInGB                  *int
	LifecycleConfig                 *string
	DisassociateLifecycleConfig     bool
	DefaultCodeRepo                 *string
	DisassociateDefaultCodeRepo     bool
	AdditionalCodeRepos             []string
	DisassociateAdditionalCodeRepos bool
	RootAccess                      *string
}

// NotebookLifecycleConfigUpdate describes an UpdateNotebookInstanceLifecycleConfig
// request; a nil script leaves it unchanged.
type NotebookLifecycleConfigUpdate struct {
	Name     string
	OnCreate *string
	OnStart  *string
}

// CodeRepositorySpec describes a Git code repository to create.
type CodeRepositorySpec struct {
	Name      string
	GitURL    string
	Branch    string
	SecretARN string
}

// CodeRepository is a registered Git repository.
type CodeRepository struct {
	Name         string
	ARN          string
	GitURL       string
	Branch       string
	SecretARN    string
	CreationTime string
}

// notebookAPI covers notebook instances and supporting resources.
type notebookAPI interface {
	CreateNotebookInstance(ctx context.Context, cfg NotebookInstanceSpec) (*NotebookInstance, error)
	DescribeNotebookInstance(ctx context.Context, name string) (*NotebookInstance, error)
	ListNotebookInstances(ctx context.Context, opts ListOptions) (ListPage[NotebookInstance], error)
	StartNotebookInstance(ctx context.Context, name string) error
	StopNotebookInstance(ctx context.Context, name string) error
	UpdateNotebookInstance(ctx context.Context, update NotebookInstanceUpdate) error
	DeleteNotebookInstance(ctx context.Context, name string) error

	CreateNotebookInstanceLifecycleConfig(ctx context.Context, cfg NotebookLifecycleConfigSpec) (*NotebookLifecycleConfig, error)
	DescribeNotebookInstanceLifecycleConfig(ctx context.Context, name string) (*NotebookLifecycleConfig, error)
	ListNotebookInstanceLifecycleConfigs(ctx context.Context, opts ListOptions) (ListPage[NotebookLifecycleConfig], error)
	UpdateNotebookInstanceLifecycleConfig(ctx context.Context, update NotebookLifecycleConfigUpdate) error
	DeleteNotebookInstanceLifecycleConfig(ctx context.Context, name string) error

	CreateCodeRepository(ctx context.Context, cfg CodeRepositorySpec) (*CodeRepository, error)
	DescribeCodeRepository(ctx context.Context, name string) (*CodeRepository, error)
	ListCodeRepositories(ctx context.Context, opts ListOptions) (ListPage[CodeRepository], error)
	DeleteCodeRepository(ctx context.Context, name string) error
}
