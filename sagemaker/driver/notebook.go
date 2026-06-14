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
	Tags            []Tag
}

// NotebookInstance is a managed notebook instance.
type NotebookInstance struct {
	Name             string
	ARN              string
	InstanceType     string
	RoleARN          string
	VolumeSizeInGB   int
	LifecycleConfig  string
	DefaultCodeRepo  string
	Status           string
	URL              string
	FailureReason    string
	CreationTime     string
	LastModifiedTime string
	Tags             []Tag
}

// NotebookLifecycleConfigSpec describes a lifecycle config to create.
type NotebookLifecycleConfigSpec struct {
	Name     string
	OnCreate string
	OnStart  string
}

// NotebookLifecycleConfig is a notebook lifecycle configuration.
type NotebookLifecycleConfig struct {
	Name         string
	ARN          string
	OnCreate     string
	OnStart      string
	CreationTime string
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
	ListNotebookInstances(ctx context.Context) ([]NotebookInstance, error)
	StartNotebookInstance(ctx context.Context, name string) error
	StopNotebookInstance(ctx context.Context, name string) error
	DeleteNotebookInstance(ctx context.Context, name string) error

	CreateNotebookInstanceLifecycleConfig(ctx context.Context, cfg NotebookLifecycleConfigSpec) (*NotebookLifecycleConfig, error)
	DescribeNotebookInstanceLifecycleConfig(ctx context.Context, name string) (*NotebookLifecycleConfig, error)
	ListNotebookInstanceLifecycleConfigs(ctx context.Context) ([]NotebookLifecycleConfig, error)
	DeleteNotebookInstanceLifecycleConfig(ctx context.Context, name string) error

	CreateCodeRepository(ctx context.Context, cfg CodeRepositorySpec) (*CodeRepository, error)
	DescribeCodeRepository(ctx context.Context, name string) (*CodeRepository, error)
	ListCodeRepositories(ctx context.Context) ([]CodeRepository, error)
	DeleteCodeRepository(ctx context.Context, name string) error
}
