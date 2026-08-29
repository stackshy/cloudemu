// Package driver defines the interface and types for AWS Step Functions (SFN)
// implementations. It models state machines (with their ASL definition stored
// verbatim), executions, execution history, activities, tags, and — where the
// SDK exposes them — state-machine versions and aliases.
//
// The emulator interprets the Amazon States Language with a real state-graph
// walker (see providers/aws/sfn/asl): StartExecution walks the definition from
// StartAt following Next/End, computes the true terminal status and output, and
// records a per-state event history. Execution completes synchronously; the
// settle overlay keeps RUNNING->SUCCEEDED/FAILED observable under AsyncSettle.
// The interpreter is dependency-free and deterministic (driven by config.Clock).
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

// Map Run statuses.
const (
	MapRunStatusRunning   = "RUNNING"
	MapRunStatusSucceeded = "SUCCEEDED"
	MapRunStatusFailed    = "FAILED"
	MapRunStatusAborted   = "ABORTED"
)

// Test state execution statuses.
const (
	TestStatusSucceeded = "SUCCEEDED"
	TestStatusFailed    = "FAILED"
)

// State machine definition validation result codes.
const (
	ValidationResultOK   = "OK"
	ValidationResultFail = "FAIL"
)

// Validation diagnostic severities.
const (
	ValidationSeverityError   = "ERROR"
	ValidationSeverityWarning = "WARNING"
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
	// History is the full per-state event list the interpreter produced, stored
	// on the execution so it round-trips through snapshot/persist for free.
	History []HistoryEvent
}

// HistoryEvent is one entry in an execution's event history.
type HistoryEvent struct {
	ID              int64
	PreviousEventID int64
	Type            string
	Timestamp       time.Time
	// StateName names the state a StateEntered/StateExited event refers to.
	StateName string
	// Input is set on the ExecutionStarted and StateEntered events; Output on
	// ExecutionSucceeded and StateExited events.
	Input  string
	Output string
	// Error and Cause are set on failure/abort events (ExecutionFailed,
	// FailStateEntered, ExecutionAborted, LambdaFunctionFailed).
	Error string
	Cause string
	// Resource names the integration a task sub-event targets (e.g. the Task
	// Resource ARN on a LambdaFunctionScheduled event).
	Resource string
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

// MapRunCounts holds the per-status tallies shared by a Map Run's execution
// counts and item counts. Every field is a plain count of child workflow
// executions (or items) in the corresponding state.
type MapRunCounts struct {
	Aborted        int64
	Failed         int64
	Pending        int64
	ResultsWritten int64
	Running        int64
	Succeeded      int64
	TimedOut       int64
	Total          int64
}

// MapRun models a distributed-map child-execution run. The emulator does not
// interpret Amazon States Language, so Map Runs are not produced by executing a
// state machine; SeedMapRun populates one for describe/list/update to operate on.
type MapRun struct {
	ARN                        string
	ExecutionArn               string
	StateMachineArn            string
	Status                     string
	MaxConcurrency             int32
	ToleratedFailureCount      int64
	ToleratedFailurePercentage float32
	RedriveCount               int32
	StartDate                  time.Time
	StopDate                   time.Time
	RedriveDate                time.Time
	ExecutionCounts            MapRunCounts
	ItemCounts                 MapRunCounts
}

// UpdateMapRunInput mutates the concurrency and failure tolerances of a Map Run.
// Nil pointers leave the corresponding field unchanged.
type UpdateMapRunInput struct {
	MapRunArn                  string
	MaxConcurrency             *int32
	ToleratedFailureCount      *int64
	ToleratedFailurePercentage *float32
}

// RedriveResult reports the effect of RedriveExecution.
type RedriveResult struct {
	RedriveDate time.Time
}

// TestStateInput evaluates a single state definition against an input.
type TestStateInput struct {
	Definition string
	Input      string
}

// TestStateResult echoes the outcome of a TestState evaluation. The emulator
// runs no interpreter: a valid, non-empty definition succeeds and echoes input.
type TestStateResult struct {
	Output    string
	Status    string
	NextState string
	Error     string
	Cause     string
}

// ValidationDiagnostic is one finding from ValidateStateMachineDefinition.
type ValidationDiagnostic struct {
	Severity string
	Code     string
	Message  string
	Location string
}

// ValidationResult reports the outcome of ValidateStateMachineDefinition.
type ValidationResult struct {
	Result      string
	Diagnostics []ValidationDiagnostic
	Truncated   bool
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
	// StopExecution aborts a still-settling execution, persisting the caller's
	// errCode/cause onto the execution's Error/Cause fields.
	StopExecution(ctx context.Context, arn, errCode, cause string) (time.Time, error)
	ListExecutions(ctx context.Context, stateMachineArn, statusFilter string) ([]Execution, error)
	GetExecutionHistory(ctx context.Context, arn string, reverse bool) ([]HistoryEvent, error)
	DescribeStateMachineForExecution(ctx context.Context, executionArn string) (*StateMachine, error)
	RedriveExecution(ctx context.Context, arn string) (*RedriveResult, error)

	// Map Runs (distributed-map child executions).
	DescribeMapRun(ctx context.Context, mapRunArn string) (*MapRun, error)
	ListMapRuns(ctx context.Context, executionArn string) ([]MapRun, error)
	UpdateMapRun(ctx context.Context, in UpdateMapRunInput) error

	// Definition tooling.
	TestState(ctx context.Context, in TestStateInput) (*TestStateResult, error)
	ValidateStateMachineDefinition(ctx context.Context, definition, smType string) (*ValidationResult, error)

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
