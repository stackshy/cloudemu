// Package driver defines the interface and types for the AWS Fault Injection
// Simulator (FIS) control-plane API: experiment templates and the experiments
// started from them.
//
// The emulator is control-plane only: it does NOT inject any real faults. An
// experiment template is created synchronously with stable computed fields (id,
// arn, creationTime and lastUpdateTime) minted once at create and stored, so
// repeated GetExperimentTemplate and ListExperimentTemplates reads never drift.
// StartExperiment materializes an experiment from a template — copying its
// actions, targets, stop conditions, role and log configuration verbatim — and
// places it directly in the running state (there is no data plane to advance it
// to completion); StopExperiment moves a running experiment to the stopped
// terminal state. The experiment id, arn, state, creationTime and startTime are
// likewise minted once and stable across reads.
package driver

import (
	"context"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// TargetFilter narrows a target's resources by an attribute path and values.
type TargetFilter struct {
	Path   string   `json:"path,omitempty"`
	Values []string `json:"values,omitempty"`
}

// Target is an experiment target: the set of resources an action operates on.
// The block round-trips verbatim between create/update and read.
type Target struct {
	ResourceType  string            `json:"resourceType,omitempty"`
	ResourceArns  []string          `json:"resourceArns,omitempty"`
	ResourceTags  map[string]string `json:"resourceTags,omitempty"`
	Filters       []TargetFilter    `json:"filters,omitempty"`
	Parameters    map[string]string `json:"parameters,omitempty"`
	SelectionMode string            `json:"selectionMode,omitempty"`
}

// Action is an experiment action. The block round-trips verbatim.
type Action struct {
	ActionID    string            `json:"actionId,omitempty"`
	Description string            `json:"description,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	Targets     map[string]string `json:"targets,omitempty"`
	StartAfter  []string          `json:"startAfter,omitempty"`
}

// StopCondition is one experiment stop condition. The block round-trips verbatim.
type StopCondition struct {
	Source string `json:"source,omitempty"`
	Value  string `json:"value,omitempty"`
}

// CloudWatchLogsConfiguration configures experiment logging to CloudWatch Logs.
type CloudWatchLogsConfiguration struct {
	LogGroupArn string `json:"logGroupArn,omitempty"`
}

// S3Configuration configures experiment logging to Amazon S3.
type S3Configuration struct {
	BucketName string `json:"bucketName,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
}

// LogConfiguration is the experiment logging configuration.
type LogConfiguration struct {
	LogSchemaVersion            int32                        `json:"logSchemaVersion,omitempty"`
	CloudWatchLogsConfiguration *CloudWatchLogsConfiguration `json:"cloudWatchLogsConfiguration,omitempty"`
	S3Configuration             *S3Configuration             `json:"s3Configuration,omitempty"`
}

// ExperimentOptions are the experiment options. AccountTargeting and
// EmptyTargetResolutionMode apply to both templates and experiments; ActionsMode
// is set only on an experiment via StartExperiment.
type ExperimentOptions struct {
	AccountTargeting          string `json:"accountTargeting,omitempty"`
	EmptyTargetResolutionMode string `json:"emptyTargetResolutionMode,omitempty"`
	ActionsMode               string `json:"actionsMode,omitempty"`
}

// ExperimentTemplate is a FIS experiment template. ID, Arn, CreationTime and
// LastUpdateTime are computed once at create (LastUpdateTime advances on update)
// and stable across reads.
type ExperimentTemplate struct {
	ID                string
	Arn               string
	Description       string
	RoleArn           string
	Actions           map[string]Action
	Targets           map[string]Target
	StopConditions    []StopCondition
	LogConfiguration  *LogConfiguration
	ExperimentOptions ExperimentOptions
	Tags              map[string]string
	CreationTime      time.Time
	LastUpdateTime    time.Time
}

// ExperimentState is the state of an experiment.
type ExperimentState struct {
	Status string
	Reason string
}

// ExperimentActionState is the state of a single experiment action.
type ExperimentActionState struct {
	Status string
	Reason string
}

// ExperimentAction is an action within a running or completed experiment: the
// template action plus its per-experiment execution state and timing.
type ExperimentAction struct {
	ActionID    string
	Description string
	Parameters  map[string]string
	Targets     map[string]string
	StartAfter  []string
	State       ExperimentActionState
	StartTime   time.Time
	EndTime     time.Time
}

// Experiment is a FIS experiment started from a template. ID, Arn, State,
// CreationTime and StartTime are minted once and stable across reads.
type Experiment struct {
	ID                   string
	Arn                  string
	ExperimentTemplateID string
	RoleArn              string
	State                ExperimentState
	Actions              map[string]ExperimentAction
	Targets              map[string]Target
	StopConditions       []StopCondition
	LogConfiguration     *LogConfiguration
	ExperimentOptions    ExperimentOptions
	Tags                 map[string]string
	CreationTime         time.Time
	StartTime            time.Time
	EndTime              time.Time
}

// CreateExperimentTemplateInput is the input to CreateExperimentTemplate.
type CreateExperimentTemplateInput struct {
	ClientToken       string
	Description       string
	RoleArn           string
	Actions           map[string]Action
	Targets           map[string]Target
	StopConditions    []StopCondition
	LogConfiguration  *LogConfiguration
	ExperimentOptions *ExperimentOptions
	Tags              map[string]string
}

// UpdateExperimentTemplateInput is the input to UpdateExperimentTemplate. A nil
// pointer field means the member was absent from the request and is left
// unchanged; a non-nil pointer replaces the value.
type UpdateExperimentTemplateInput struct {
	ID                string
	Description       *string
	RoleArn           *string
	Actions           *map[string]Action
	Targets           *map[string]Target
	StopConditions    *[]StopCondition
	LogConfiguration  *LogConfiguration
	ExperimentOptions *ExperimentOptions
}

// StartExperimentInput is the input to StartExperiment.
type StartExperimentInput struct {
	ClientToken          string
	ExperimentTemplateID string
	ActionsMode          string
	Tags                 map[string]string
}

// FIS is the Fault Injection Simulator control-plane surface: experiment
// templates, experiments and resource tags.
type FIS interface {
	CreateExperimentTemplate(ctx context.Context, in *CreateExperimentTemplateInput) (*ExperimentTemplate, error)
	GetExperimentTemplate(ctx context.Context, id string) (*ExperimentTemplate, error)
	UpdateExperimentTemplate(ctx context.Context, in *UpdateExperimentTemplateInput) (*ExperimentTemplate, error)
	DeleteExperimentTemplate(ctx context.Context, id string) (*ExperimentTemplate, error)
	ListExperimentTemplates(ctx context.Context, page Page) (templates []*ExperimentTemplate, nextToken string, err error)

	StartExperiment(ctx context.Context, in *StartExperimentInput) (*Experiment, error)
	StopExperiment(ctx context.Context, id string) (*Experiment, error)
	GetExperiment(ctx context.Context, id string) (*Experiment, error)
	ListExperiments(ctx context.Context, page Page) (experiments []*Experiment, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
