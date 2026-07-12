package driver

import "context"

// CustomJobConfig describes a custom training job.
type CustomJobConfig struct {
	Location       string
	DisplayName    string
	MachineType    string
	ReplicaCount   int
	ContainerImage string
}

// CustomJob is a custom training job (create is synchronous; poll State).
type CustomJob struct {
	Name        string // projects/{p}/locations/{l}/customJobs/{id}
	DisplayName string
	State       string
	CreateTime  string
	EndTime     string
}

// BatchPredictionJobConfig describes a batch prediction job.
type BatchPredictionJobConfig struct {
	Location        string
	DisplayName     string
	Model           string
	InputURI        string
	OutputURIPrefix string
}

// BatchPredictionJob is an offline prediction job (create is synchronous).
type BatchPredictionJob struct {
	Name        string // projects/{p}/locations/{l}/batchPredictionJobs/{id}
	DisplayName string
	Model       string
	State       string
	CreateTime  string
	EndTime     string
}

// HyperparameterTuningJobConfig describes an HPO job.
type HyperparameterTuningJobConfig struct {
	Location       string
	DisplayName    string
	MaxTrialCount  int
	ParallelTrials int
}

// HyperparameterTuningJob is an HPO job (create is synchronous).
type HyperparameterTuningJob struct {
	Name          string // projects/{p}/locations/{l}/hyperparameterTuningJobs/{id}
	DisplayName   string
	State         string
	MaxTrialCount int
	CreateTime    string
	EndTime       string
}

// jobsAPI covers the synchronous-create job family (poll State after create;
// cancel returns no body).
type jobsAPI interface {
	CreateCustomJob(ctx context.Context, cfg CustomJobConfig) (*CustomJob, error)
	GetCustomJob(ctx context.Context, name string) (*CustomJob, error)
	ListCustomJobs(ctx context.Context, location string) ([]CustomJob, error)
	CancelCustomJob(ctx context.Context, name string) error
	DeleteCustomJob(ctx context.Context, name string) (*Operation, error)

	CreateBatchPredictionJob(ctx context.Context, cfg BatchPredictionJobConfig) (*BatchPredictionJob, error)
	GetBatchPredictionJob(ctx context.Context, name string) (*BatchPredictionJob, error)
	ListBatchPredictionJobs(ctx context.Context, location string) ([]BatchPredictionJob, error)
	CancelBatchPredictionJob(ctx context.Context, name string) error
	DeleteBatchPredictionJob(ctx context.Context, name string) (*Operation, error)

	CreateHyperparameterTuningJob(ctx context.Context, cfg HyperparameterTuningJobConfig) (*HyperparameterTuningJob, error)
	GetHyperparameterTuningJob(ctx context.Context, name string) (*HyperparameterTuningJob, error)
	ListHyperparameterTuningJobs(ctx context.Context, location string) ([]HyperparameterTuningJob, error)
	CancelHyperparameterTuningJob(ctx context.Context, name string) error
}
