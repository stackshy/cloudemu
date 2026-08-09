// Package driver defines the interface and types for AWS Step Functions (SFN)
// implementations. It models state machines (with their ASL definition stored
// verbatim), executions, execution history, activities, tags, and — where the
// SDK exposes them — state-machine versions and aliases.
//
// The emulator does NOT interpret the Amazon States Language: StartExecution
// completes an execution immediately (RUNNING then SUCCEEDED) with output
// echoing the input, and GetExecutionHistory synthesizes a minimal but valid
// event list. This keeps behavior deterministic and dependency-free while
// preserving the SDK wire shapes.
package driver

import (
	"context"
	"time"
)

// State machine types.
const (
	TypeStandard = "STANDARD"
	TypeExpress  = "EXPRESS"
)

// State machine statuses.
const (
	SMStatusActive   = "ACTIVE"
	SMStatusDeleting = "DELETING"
)

// Execution statuses.
const (
	ExecStatusRunning   = "RUNNING"
	ExecStatusSucceeded = "SUCCEEDED"
	ExecStatusFailed    = "FAILED"
	ExecStatusTimedOut  = "TIMED_OUT"
	ExecStatusAborted   = "ABORTED"
)

// StateMachine is the full description of a Step Functions state machine.
type StateMachine struct {
	ARN               string
	Name              string
	Definition        string // ASL JSON, stored verbatim
	RoleArn           string
	Type              string
	Status            string
	Description       string
	RevisionID        string
	Label             string
	CreationDate      time.Time
	Tags              map[string]string
	LatestVersionArn  string
	PublishedVersions []Version
	LoggingConfigJSON string
	TracingConfigJSON string
	EncryptionCfgJSON string
}

// Version is a published, immutable snapshot of a state machine.
type Version struct {
	ARN          string
	Description  string
	Definition   string
	RoleArn      string
	RevisionID   string
	CreationDate time.Time
}

// Alias is a named pointer to one or more state machine versions.
type Alias struct {
	ARN          string
	Name         string
	Description  string
	Routing      []RouteEntry
	CreationDate time.Time
	UpdateDate   time.Time
}

// RouteEntry weights traffic to a state machine version.
type RouteEntry struct {
	StateMachineVersionArn string
	Weight                 int32
}

// Execution is a single run of a state machine.
type Execution struct {
	ARN             string
	Name            string
	StateMachineArn string
	Status          string
	Input           string
	Output          string
	Error           string
	Cause           string
	StartDate       time.Time
	StopDate        time.Time
}

// HistoryEvent is one entry in an execution's event history.
type HistoryEvent struct {
	ID              int64
	PreviousEventID int64
	Type            string
	Timestamp       time.Time
	// Input is set on the ExecutionStarted event; Output on ExecutionSucceeded.
	Input  string
	Output string
}

// Activity is a task worker registration.
type Activity struct {
	ARN          string
	Name         string
	CreationDate time.Time
	Tags         map[string]string
}

// CreateStateMachineInput describes a state machine to create.
type CreateStateMachineInput struct {
	Name              string
	Definition        string
	RoleArn           string
	Type              string
	Description       string
	Publish           bool
	Tags              map[string]string
	LoggingConfigJSON string
	TracingConfigJSON string
	EncryptionCfgJSON string
}

// UpdateStateMachineInput describes a mutation of an existing state machine.
type UpdateStateMachineInput struct {
	ARN               string
	Definition        string
	RoleArn           string
	Publish           bool
	VersionDesc       string
	LoggingConfigJSON string
	TracingConfigJSON string
	EncryptionCfgJSON string
}

// UpdateStateMachineResult reports the effect of UpdateStateMachine.
type UpdateStateMachineResult struct {
	UpdateDate             time.Time
	RevisionID             string
	StateMachineVersionArn string
}

// StartExecutionInput starts a new execution.
type StartExecutionInput struct {
	StateMachineArn string
	Name            string
	Input           string
}

// SFN is the interface a Step Functions backend implements.
type SFN interface {
	// State machines.
	CreateStateMachine(ctx context.Context, in CreateStateMachineInput) (arn, versionArn string, created time.Time, err error)
	DescribeStateMachine(ctx context.Context, arn string) (*StateMachine, error)
	UpdateStateMachine(ctx context.Context, in UpdateStateMachineInput) (*UpdateStateMachineResult, error)
	DeleteStateMachine(ctx context.Context, arn string) error
	ListStateMachines(ctx context.Context) ([]StateMachine, error)

	// Executions.
	StartExecution(ctx context.Context, in StartExecutionInput) (*Execution, error)
	StartSyncExecution(ctx context.Context, in StartExecutionInput) (*Execution, error)
	DescribeExecution(ctx context.Context, arn string) (*Execution, error)
	StopExecution(ctx context.Context, arn, errCode, cause string) (time.Time, error)
	ListExecutions(ctx context.Context, stateMachineArn, statusFilter string) ([]Execution, error)
	GetExecutionHistory(ctx context.Context, arn string, reverse bool) ([]HistoryEvent, error)
	DescribeStateMachineForExecution(ctx context.Context, executionArn string) (*StateMachine, error)

	// Versions and aliases.
	PublishStateMachineVersion(ctx context.Context, arn, description string) (versionArn string, created time.Time, err error)
	ListStateMachineVersions(ctx context.Context, arn string) ([]Version, error)
	DeleteStateMachineVersion(ctx context.Context, versionArn string) error
	CreateStateMachineAlias(ctx context.Context, name, description string, routing []RouteEntry) (arn string, created time.Time, err error)
	DescribeStateMachineAlias(ctx context.Context, arn string) (*Alias, error)
	UpdateStateMachineAlias(ctx context.Context, arn, description string, routing []RouteEntry) (time.Time, error)
	DeleteStateMachineAlias(ctx context.Context, arn string) error
	ListStateMachineAliases(ctx context.Context, stateMachineArn string) ([]Alias, error)

	// Activities.
	CreateActivity(ctx context.Context, name string, tags map[string]string) (arn string, created time.Time, err error)
	DescribeActivity(ctx context.Context, arn string) (*Activity, error)
	DeleteActivity(ctx context.Context, arn string) error
	ListActivities(ctx context.Context) ([]Activity, error)
	GetActivityTask(ctx context.Context, activityArn, workerName string) (taskToken, input string, err error)
	SendTaskSuccess(ctx context.Context, taskToken, output string) error
	SendTaskFailure(ctx context.Context, taskToken, errCode, cause string) error
	SendTaskHeartbeat(ctx context.Context, taskToken string) error

	// Tags.
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
