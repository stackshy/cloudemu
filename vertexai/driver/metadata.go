package driver

import "context"

// MetadataStore is an ML-metadata store.
type MetadataStore struct {
	Name       string // projects/{p}/locations/{l}/metadataStores/{id}
	CreateTime string
}

// Tensorboard is a managed Tensorboard instance.
type Tensorboard struct {
	Name        string // projects/{p}/locations/{l}/tensorboards/{id}
	DisplayName string
	CreateTime  string
}

// Schedule runs a pipeline job on a cron schedule.
type Schedule struct {
	Name        string // projects/{p}/locations/{l}/schedules/{id}
	DisplayName string
	Cron        string
	State       string // ACTIVE | PAUSED | COMPLETED
	CreateTime  string
}

// NotebookRuntimeTemplate is a template for notebook runtimes.
type NotebookRuntimeTemplate struct {
	Name        string // projects/{p}/locations/{l}/notebookRuntimeTemplates/{id}
	DisplayName string
	MachineType string
	CreateTime  string
}

// NotebookRuntime is an assigned notebook runtime.
type NotebookRuntime struct {
	Name         string // projects/{p}/locations/{l}/notebookRuntimes/{id}
	DisplayName  string
	RuntimeState string // RUNNING | STOPPED
	CreateTime   string
}

// metadataAPI covers ML metadata stores, Tensorboard, schedules, and notebook
// runtimes/templates.
type metadataAPI interface {
	CreateMetadataStore(ctx context.Context, location, storeID string) (*Operation, *MetadataStore, error)
	GetMetadataStore(ctx context.Context, name string) (*MetadataStore, error)
	ListMetadataStores(ctx context.Context, location string) ([]MetadataStore, error)
	DeleteMetadataStore(ctx context.Context, name string) (*Operation, error)

	CreateTensorboard(ctx context.Context, location, displayName string) (*Operation, *Tensorboard, error)
	GetTensorboard(ctx context.Context, name string) (*Tensorboard, error)
	ListTensorboards(ctx context.Context, location string) ([]Tensorboard, error)
	DeleteTensorboard(ctx context.Context, name string) (*Operation, error)

	CreateSchedule(ctx context.Context, location, displayName, cron string) (*Schedule, error)
	GetSchedule(ctx context.Context, name string) (*Schedule, error)
	ListSchedules(ctx context.Context, location string) ([]Schedule, error)
	PauseSchedule(ctx context.Context, name string) error
	ResumeSchedule(ctx context.Context, name string) error
	DeleteSchedule(ctx context.Context, name string) (*Operation, error)

	CreateNotebookRuntimeTemplate(ctx context.Context, location, displayName, machineType string) (*Operation, *NotebookRuntimeTemplate, error)
	GetNotebookRuntimeTemplate(ctx context.Context, name string) (*NotebookRuntimeTemplate, error)
	ListNotebookRuntimeTemplates(ctx context.Context, location string) ([]NotebookRuntimeTemplate, error)
	DeleteNotebookRuntimeTemplate(ctx context.Context, name string) (*Operation, error)

	AssignNotebookRuntime(ctx context.Context, location, displayName string) (*Operation, *NotebookRuntime, error)
	GetNotebookRuntime(ctx context.Context, name string) (*NotebookRuntime, error)
	ListNotebookRuntimes(ctx context.Context, location string) ([]NotebookRuntime, error)
	StartNotebookRuntime(ctx context.Context, name string) (*Operation, error)
	StopNotebookRuntime(ctx context.Context, name string) (*Operation, error)
	DeleteNotebookRuntime(ctx context.Context, name string) (*Operation, error)
}
