// Package driver defines the interface for Amazon SageMaker AI: training,
// processing, transform, tuning, AutoML, labeling and compilation jobs; the
// model/endpoint inference stack; the model registry; Studio; notebook
// instances; HyperPod clusters; Feature Store; and pipelines.
//
// The interface uses plain Go types only (no AWS SDK dependencies). It spans
// the control plane and the inference runtime (InvokeEndpoint), and the
// Feature Store online runtime (PutRecord/GetRecord).
package driver

import "context"

// Tag is a single resource tag.
type Tag struct {
	Key   string
	Value string
}

// SageMaker is the interface that SageMaker AI control-plane implementations
// must satisfy. It is composed of one segment per resource family so the
// surface stays navigable as it grows.
type SageMaker interface {
	jobsAPI
	inferenceAPI
	registryAPI
	studioAPI
	notebookAPI
	clusterAPI
	featureStoreAPI
	pipelineAPI
	tagsAPI
}

// tagsAPI covers AddTags/ListTags/DeleteTags, which apply to most resources by
// ARN.
type tagsAPI interface {
	AddTags(ctx context.Context, resourceARN string, tags []Tag) ([]Tag, error)
	ListTags(ctx context.Context, resourceARN string) ([]Tag, error)
	DeleteTags(ctx context.Context, resourceARN string, keys []string) error
}

// Runtime is the SageMaker inference runtime (the sagemaker-runtime service):
// synchronous and asynchronous endpoint invocation.
type Runtime interface {
	InvokeEndpoint(ctx context.Context, in InvokeEndpointInput) (*InvokeEndpointOutput, error)
	InvokeEndpointAsync(ctx context.Context, in InvokeEndpointAsyncInput) (*InvokeEndpointAsyncOutput, error)
}

// Service is the union of the SageMaker control plane and the inference
// runtime. The in-memory mock satisfies it, letting a single SDK-compat HTTP
// handler serve both the sagemaker and sagemaker-runtime SDK clients.
type Service interface {
	SageMaker
	Runtime
}
