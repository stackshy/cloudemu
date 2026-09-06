// Package driver defines the interface and types for AWS Batch implementations.
// It models the Batch control plane: compute environments, job queues, and
// job-definition revisions, plus resource tagging.
//
// The interface is deliberately narrow — a backend stores what it is given and
// echoes it back, generating ARNs, ECS cluster ARNs, and job-definition
// revisions, and provisioning synchronously (a created resource is VALID/ACTIVE
// immediately). Deeply nested request payloads (computeResources,
// containerProperties, …) are carried as raw JSON so they round-trip with no
// injected defaults.
package driver

import "context"

// Batch is the interface an AWS Batch backend implements.
type Batch interface {
	// Compute environments.
	CreateComputeEnvironment(ctx context.Context, in CreateComputeEnvironmentInput) (*ComputeEnvironment, error)
	DescribeComputeEnvironments(ctx context.Context, names []string) ([]ComputeEnvironment, error)
	UpdateComputeEnvironment(ctx context.Context, in UpdateComputeEnvironmentInput) (*ComputeEnvironment, error)
	DeleteComputeEnvironment(ctx context.Context, name string) error

	// Job queues.
	CreateJobQueue(ctx context.Context, in CreateJobQueueInput) (*JobQueue, error)
	DescribeJobQueues(ctx context.Context, names []string) ([]JobQueue, error)
	UpdateJobQueue(ctx context.Context, in UpdateJobQueueInput) (*JobQueue, error)
	DeleteJobQueue(ctx context.Context, name string) error

	// Job definitions.
	RegisterJobDefinition(ctx context.Context, in RegisterJobDefinitionInput) (*JobDefinition, error)
	DescribeJobDefinitions(ctx context.Context, in DescribeJobDefinitionsInput) ([]JobDefinition, error)
	DeregisterJobDefinition(ctx context.Context, nameRevisionOrARN string) error

	// Tags (by ARN), shared across all three resource kinds.
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
