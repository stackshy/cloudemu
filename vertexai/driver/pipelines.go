package driver

import "context"

// TrainingPipelineConfig describes a training pipeline.
type TrainingPipelineConfig struct {
	Location    string
	DisplayName string
	TaskType    string // AutoML task definition or custom
}

// TrainingPipeline wraps AutoML/custom training (create is synchronous).
type TrainingPipeline struct {
	Name        string // projects/{p}/locations/{l}/trainingPipelines/{id}
	DisplayName string
	State       string
	CreateTime  string
	EndTime     string
}

// PipelineJobConfig describes a Kubeflow/TFX pipeline run.
type PipelineJobConfig struct {
	Location    string
	DisplayName string
	TemplateURI string
}

// PipelineJob is a pipeline execution (create is synchronous).
type PipelineJob struct {
	Name        string // projects/{p}/locations/{l}/pipelineJobs/{id}
	DisplayName string
	State       string
	CreateTime  string
	EndTime     string
}

// pipelinesAPI covers training pipelines and pipeline jobs.
type pipelinesAPI interface {
	CreateTrainingPipeline(ctx context.Context, cfg TrainingPipelineConfig) (*TrainingPipeline, error)
	GetTrainingPipeline(ctx context.Context, name string) (*TrainingPipeline, error)
	ListTrainingPipelines(ctx context.Context, location string) ([]TrainingPipeline, error)
	CancelTrainingPipeline(ctx context.Context, name string) error
	DeleteTrainingPipeline(ctx context.Context, name string) (*Operation, error)

	CreatePipelineJob(ctx context.Context, cfg PipelineJobConfig) (*PipelineJob, error)
	GetPipelineJob(ctx context.Context, name string) (*PipelineJob, error)
	ListPipelineJobs(ctx context.Context, location string) ([]PipelineJob, error)
	CancelPipelineJob(ctx context.Context, name string) error
	DeletePipelineJob(ctx context.Context, name string) (*Operation, error)
}
