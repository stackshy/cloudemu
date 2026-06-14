package driver

import "context"

// Pipeline status values.
const (
	PipelineActive   = "Active"
	PipelineDeleting = "Deleting"
)

// Pipeline execution status values.
const (
	ExecutionExecuting = "Executing"
	ExecutionStopping  = "Stopping"
	ExecutionStopped   = "Stopped"
	ExecutionFailed    = "Failed"
	ExecutionSucceeded = "Succeeded"
)

// Experiment / trial component primary status values.
const (
	ComponentInProgress = "InProgress"
	ComponentCompleted  = "Completed"
	ComponentFailed     = "Failed"
)

// PipelineSpec describes a pipeline to create.
type PipelineSpec struct {
	PipelineName string
	RoleARN      string
	Definition   string // JSON pipeline definition
	Tags         []Tag
}

// Pipeline is a SageMaker pipeline.
type Pipeline struct {
	PipelineName     string
	PipelineARN      string
	RoleARN          string
	Definition       string
	Status           string
	CreationTime     string
	LastModifiedTime string
	Tags             []Tag
}

// PipelineExecution is one run of a pipeline.
type PipelineExecution struct {
	ExecutionARN string
	PipelineName string
	Status       string
	StartTime    string
	EndTime      string
}

// ExperimentSpec describes an experiment to create.
type ExperimentSpec struct {
	ExperimentName string
	Description    string
	Tags           []Tag
}

// Experiment groups related trials.
type Experiment struct {
	ExperimentName string
	ExperimentARN  string
	Description    string
	CreationTime   string
	Tags           []Tag
}

// TrialSpec describes a trial to create.
type TrialSpec struct {
	TrialName      string
	ExperimentName string
	Tags           []Tag
}

// Trial is a single trial within an experiment.
type Trial struct {
	TrialName      string
	TrialARN       string
	ExperimentName string
	CreationTime   string
	Tags           []Tag
}

// pipelineAPI covers pipelines (+ executions) and experiments/trials.
type pipelineAPI interface {
	CreatePipeline(ctx context.Context, cfg PipelineSpec) (*Pipeline, error)
	DescribePipeline(ctx context.Context, name string) (*Pipeline, error)
	ListPipelines(ctx context.Context) ([]Pipeline, error)
	UpdatePipeline(ctx context.Context, name, definition string) (*Pipeline, error)
	DeletePipeline(ctx context.Context, name string) error

	StartPipelineExecution(ctx context.Context, pipelineName string) (*PipelineExecution, error)
	DescribePipelineExecution(ctx context.Context, executionARN string) (*PipelineExecution, error)
	ListPipelineExecutions(ctx context.Context, pipelineName string) ([]PipelineExecution, error)
	StopPipelineExecution(ctx context.Context, executionARN string) error

	CreateExperiment(ctx context.Context, cfg ExperimentSpec) (*Experiment, error)
	DescribeExperiment(ctx context.Context, name string) (*Experiment, error)
	ListExperiments(ctx context.Context) ([]Experiment, error)
	DeleteExperiment(ctx context.Context, name string) error

	CreateTrial(ctx context.Context, cfg TrialSpec) (*Trial, error)
	DescribeTrial(ctx context.Context, name string) (*Trial, error)
	ListTrials(ctx context.Context, experimentName string) ([]Trial, error)
	DeleteTrial(ctx context.Context, name string) error
}
