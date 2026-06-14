// Package driver defines the interface for Google Cloud Vertex AI
// (aiplatform.googleapis.com): datasets, the model registry, endpoints and
// online prediction, custom/training/tuning jobs, batch prediction, pipelines,
// the Gemini generateContent runtime, Feature Store, Vector Search,
// Tensorboard, ML metadata, schedules and notebook runtimes.
//
// The interface uses plain Go types only (no cloud SDK dependencies). Resource
// names follow Vertex's REST convention
// projects/{project}/locations/{location}/{collection}/{id}. Long-running
// control-plane mutations are modeled as completing synchronously; the
// job-family resources expose a state field that the mock drives straight to a
// terminal success state.
package driver

import "context"

// Job state values (JobState enum) shared by custom/batch/HPO jobs.
const (
	JobStateQueued    = "JOB_STATE_QUEUED"
	JobStatePending   = "JOB_STATE_PENDING"
	JobStateRunning   = "JOB_STATE_RUNNING"
	JobStateSucceeded = "JOB_STATE_SUCCEEDED"
	JobStateFailed    = "JOB_STATE_FAILED"
	JobStateCancelled = "JOB_STATE_CANCELED"
)

// Pipeline state values (PipelineState enum) used by trainingPipelines and
// pipelineJobs.
const (
	PipelineStateRunning   = "PIPELINE_STATE_RUNNING"
	PipelineStateSucceeded = "PIPELINE_STATE_SUCCEEDED"
	PipelineStateFailed    = "PIPELINE_STATE_FAILED"
	PipelineStateCancelled = "PIPELINE_STATE_CANCELED"
)

// VertexAI is the interface that Vertex AI implementations must satisfy. It is
// composed of one segment per resource family so the surface stays navigable.
type VertexAI interface {
	datasetsAPI
	modelsAPI
	endpointsAPI
	jobsAPI
	pipelinesAPI
	genAIAPI
	featureStoreAPI
	vectorSearchAPI
	metadataAPI
	operationsAPI
}

// operationsAPI exposes the long-running-operation polling surface. Every
// control-plane mutation in this emulator returns an already-done Operation,
// but clients still GET it, so it must be retrievable.
type operationsAPI interface {
	GetOperation(ctx context.Context, name string) (*Operation, error)
	ListOperations(ctx context.Context, parent string) ([]Operation, error)
}
