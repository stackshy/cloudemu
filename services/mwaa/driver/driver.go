// Package driver defines the interface and types for the Amazon Managed
// Workflows for Apache Airflow (MWAA) control-plane API. It models MWAA
// environments plus resource tagging and the CLI/web login tokens.
//
// The emulator models the fields clients and IaC tools read back (the computed
// arn, status, webserverUrl, serviceRoleArn, createdAt and per-log-type
// cloudWatchLogGroupArn) and carries the environment's configuration blocks it
// does not interpret (networkConfiguration, loggingConfiguration,
// airflowConfigurationOptions, the worker/scheduler sizing, the S3 code paths)
// verbatim as map[string]json.RawMessage, so a round-tripped environment
// reflects everything the caller sent — a bool that must round-trip false and
// an int that must round-trip 0 both survive because the raw JSON is preserved.
// Running Apache Airflow and executing DAGs are out of scope for this
// control-plane surface: an environment is created directly in the AVAILABLE
// state so IaC waiters that block on status do not hang.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Environment status values. A newly created environment is AVAILABLE so
// Terraform's create waiter (which blocks until status is AVAILABLE) completes
// without a ~20-minute real-cloud provisioning wait.
const (
	StatusCreating    = "CREATING"
	StatusAvailable   = "AVAILABLE"
	StatusUpdating    = "UPDATING"
	StatusDeleting    = "DELETING"
	StatusCreateFail  = "CREATE_FAILED"
	StatusUnavailable = "UNAVAILABLE"
)

// LastUpdate status values reported on an environment's LastUpdate block.
const (
	UpdateStatusSuccess    = "SUCCESS"
	UpdateStatusPending    = "PENDING"
	UpdateStatusProcessing = "PROCESSING"
	UpdateStatusFailed     = "FAILED"
)

// Environment is an MWAA environment. Arn, Status, WebserverUrl, ServiceRoleArn
// and CreatedAt are computed once at create and never regenerated, so repeated
// GetEnvironment/ListEnvironments reads never drift. Config carries the request
// body's configuration blocks verbatim (with the per-log-type
// cloudWatchLogGroupArn injected into LoggingConfiguration), so a round-tripped
// environment reflects exactly what the caller sent.
type Environment struct {
	Name           string
	Arn            string
	Status         string
	WebserverURL   string
	ServiceRoleArn string
	CreatedAt      time.Time
	LastUpdatedAt  time.Time
	Tags           map[string]string
	Config         map[string]json.RawMessage
}

// CreateEnvironmentInput is the input to CreateEnvironment. Config carries the
// configuration blocks the emulator round-trips verbatim; Tags is modeled
// separately because it is also mutated by the tagging operations.
type CreateEnvironmentInput struct {
	Name   string
	Tags   map[string]string
	Config map[string]json.RawMessage
}

// UpdateEnvironmentInput is the input to UpdateEnvironment. The Name identifies
// the environment and is not itself mutable; Config carries only the fields the
// PATCH request supplied, so unmentioned fields survive.
type UpdateEnvironmentInput struct {
	Name   string
	Config map[string]json.RawMessage
}

// MWAA is the Amazon MWAA control-plane surface: environments, resource tags,
// and the CLI/web login tokens.
type MWAA interface {
	CreateEnvironment(ctx context.Context, in *CreateEnvironmentInput) (*Environment, error)
	GetEnvironment(ctx context.Context, name string) (*Environment, error)
	UpdateEnvironment(ctx context.Context, in *UpdateEnvironmentInput) (*Environment, error)
	DeleteEnvironment(ctx context.Context, name string) error
	ListEnvironments(ctx context.Context, page Page) (names []string, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)

	CreateCliToken(ctx context.Context, name string) (token, webServerHostname string, err error)
	CreateWebLoginToken(ctx context.Context, name string) (token, webServerHostname string, err error)
}
