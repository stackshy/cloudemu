package driver

import (
	"context"
	"encoding/json"
)

// Monitoring types (the MonitoringType enum).
const (
	MonitoringDataQuality         = "DataQuality"
	MonitoringModelQuality        = "ModelQuality"
	MonitoringModelBias           = "ModelBias"
	MonitoringModelExplainability = "ModelExplainability"
)

// MonitoringKinds lists the four monitoring job definition kinds.
var MonitoringKinds = []string{ //nolint:gochecknoglobals // enum values
	MonitoringDataQuality, MonitoringModelQuality, MonitoringModelBias, MonitoringModelExplainability,
}

// Monitoring schedule status values.
const (
	SchedulePending   = "Pending"
	ScheduleScheduled = "Scheduled"
	ScheduleFailed    = "Failed"
	ScheduleStopped   = "Stopped"
)

// Monitoring execution status values.
const (
	MonitorExecPending             = "Pending"
	MonitorExecCompleted           = "Completed"
	MonitorExecCompletedViolations = "CompletedWithViolations"
	MonitorExecInProgress          = "InProgress"
	MonitorExecFailed              = "Failed"
	MonitorExecStopping            = "Stopping"
	MonitorExecStopped             = "Stopped"
)

// Monitoring alert status values.
const (
	AlertInAlert = "InAlert"
	AlertOK      = "OK"
)

// MaxMonitoringExecutionsPerSchedule bounds how many executions one read
// materializes, so a large Clock jump cannot make a read unbounded.
const MaxMonitoringExecutionsPerSchedule = 1000

// MonitoringScheduleSpec describes a monitoring schedule to create or update.
// Config is the request's MonitoringScheduleConfig, echoed back by Describe; the
// typed fields are what the provider validates and acts on.
type MonitoringScheduleSpec struct {
	Name                  string
	Type                  string
	JobDefinitionName     string // empty when the job is defined inline in Config
	EndpointName          string
	ScheduleExpression    string
	DataAnalysisStartTime string
	DataAnalysisEndTime   string
	Config                json.RawMessage
	Tags                  []Tag
}

// MonitoringExecution is one run of a monitoring schedule.
type MonitoringExecution struct {
	ScheduleName      string
	EndpointName      string
	JobDefinitionName string
	Type              string
	Status            string
	FailureReason     string
	ProcessingJobARN  string
	ScheduledTime     string
	CreationTime      string
	LastModifiedTime  string
}

// MonitoringSchedule is a model monitoring schedule.
type MonitoringSchedule struct {
	Name                  string
	ARN                   string
	Status                string
	FailureReason         string
	Type                  string
	JobDefinitionName     string
	EndpointName          string
	ScheduleExpression    string
	DataAnalysisStartTime string
	DataAnalysisEndTime   string
	Config                json.RawMessage
	CreationTime          string
	LastModifiedTime      string
	// ActiveFrom is when the schedule's current run of executions began (Create,
	// Start or Update).
	ActiveFrom    string
	LastExecution *MonitoringExecution
}

// MonitoringAlert is a monitoring alert of a schedule.
type MonitoringAlert struct {
	ScheduleName       string
	Name               string
	Status             string
	DatapointsToAlert  int
	EvaluationPeriod   int
	DashboardIndicator bool
	CreationTime       string
	LastModifiedTime   string
}

// MonitoringAlertEvent is a change of a monitoring alert's status.
type MonitoringAlertEvent struct {
	ScheduleName string
	AlertName    string
	Status       string
	CreationTime string
}

// MonitoringJobDefinitionSpec describes a monitoring job definition. Echo holds
// every member of the create request besides the typed ones; Describe returns
// them verbatim.
type MonitoringJobDefinitionSpec struct {
	Kind         string
	Name         string
	RoleARN      string
	EndpointName string
	Echo         map[string]json.RawMessage
	Tags         []Tag
}

// MonitoringJobDefinition is a data quality, model quality, model bias or model
// explainability monitoring job definition.
type MonitoringJobDefinition struct {
	Kind         string
	Name         string
	ARN          string
	RoleARN      string
	EndpointName string
	Echo         map[string]json.RawMessage
	CreationTime string
}

// monitoringAPI covers Model Monitor: schedules, executions, alerts and the four
// job definition kinds.
//
// ListMonitoringSchedules filters by opts.Filters keys EndpointName,
// MonitoringJobDefinitionName and MonitoringTypeEquals; ListMonitoringExecutions
// by those plus MonitoringScheduleName and ScheduledTimeAfter/Before;
// ListMonitoringAlertHistory by MonitoringScheduleName, MonitoringAlertName and
// StatusEquals; ListMonitoringJobDefinitions by EndpointName.
type monitoringAPI interface {
	CreateMonitoringSchedule(ctx context.Context, spec MonitoringScheduleSpec) (*MonitoringSchedule, error)
	DescribeMonitoringSchedule(ctx context.Context, name string) (*MonitoringSchedule, error)
	ListMonitoringSchedules(ctx context.Context, opts ListOptions) (ListPage[MonitoringSchedule], error)
	UpdateMonitoringSchedule(ctx context.Context, spec MonitoringScheduleSpec) (*MonitoringSchedule, error)
	DeleteMonitoringSchedule(ctx context.Context, name string) error
	StartMonitoringSchedule(ctx context.Context, name string) error
	StopMonitoringSchedule(ctx context.Context, name string) error
	ListMonitoringExecutions(ctx context.Context, opts ListOptions) (ListPage[MonitoringExecution], error)

	ListMonitoringAlerts(ctx context.Context, scheduleName string, opts ListOptions) (ListPage[MonitoringAlert], error)
	ListMonitoringAlertHistory(ctx context.Context, opts ListOptions) (ListPage[MonitoringAlertEvent], error)
	UpdateMonitoringAlert(ctx context.Context, scheduleName, alertName string, datapointsToAlert, evaluationPeriod int) (*MonitoringSchedule, error)

	CreateMonitoringJobDefinition(ctx context.Context, spec MonitoringJobDefinitionSpec) (*MonitoringJobDefinition, error)
	DescribeMonitoringJobDefinition(ctx context.Context, kind, name string) (*MonitoringJobDefinition, error)
	ListMonitoringJobDefinitions(ctx context.Context, kind string, opts ListOptions) (ListPage[MonitoringJobDefinition], error)
	DeleteMonitoringJobDefinition(ctx context.Context, kind, name string) error
}
