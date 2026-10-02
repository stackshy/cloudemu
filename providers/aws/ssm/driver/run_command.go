package driver

import (
	"context"
	"time"
)

// Command, invocation and plugin statuses, as the SSM API spells them.
const (
	CommandPending    = "Pending"
	CommandInProgress = "InProgress"
	CommandDelayed    = "Delayed"
	CommandSuccess    = "Success"
	CommandCancelled  = "Cancelled" //nolint:misspell // SSM API literal.
	CommandFailed     = "Failed"
	CommandTimedOut   = "TimedOut"
)

// CommandTarget identifies managed nodes by a Key/Values criterion, e.g.
// {Key: "tag:Name", Values: ["web"]}. It mirrors the SSM Target shape and is an
// alternative to listing InstanceIDs explicitly.
type CommandTarget struct {
	Key    string
	Values []string
}

// NotificationConfig is where Run Command sends status notifications. It is
// recorded and echoed back.
type NotificationConfig struct {
	NotificationArn    string
	NotificationEvents []string
	NotificationType   string
}

// CloudWatchOutputConfig names the log group command output goes to. It is
// recorded and echoed back.
type CloudWatchOutputConfig struct {
	LogGroupName  string
	OutputEnabled bool
}

// CommandConfig describes a Run Command send. Either InstanceIDs or Targets
// (or both) must be supplied; Targets select managed nodes by tag/attribute.
// Empty MaxConcurrency, MaxErrors and DocumentVersion take the service
// defaults (50, 0 and $DEFAULT), and a zero TimeoutSeconds is 3600.
type CommandConfig struct {
	InstanceIDs        []string
	Targets            []CommandTarget
	DocumentName       string
	DocumentVersion    string
	Comment            string
	Parameters         map[string][]string
	TimeoutSeconds     int32
	MaxConcurrency     string
	MaxErrors          string
	OutputS3Region     string
	OutputS3BucketName string
	OutputS3KeyPrefix  string
	ServiceRoleArn     string
	Notification       *NotificationConfig
	CloudWatchOutput   *CloudWatchOutputConfig
}

// Command is a sent command as SendCommand and ListCommands report it. Status
// and the counts reflect the time it is read.
type Command struct {
	CommandID             string
	DocumentName          string
	DocumentVersion       string
	Comment               string
	ExpiresAfter          time.Time
	Parameters            map[string][]string
	InstanceIDs           []string
	Targets               []CommandTarget
	RequestedDateTime     time.Time
	Status                string
	StatusDetails         string
	OutputS3Region        string
	OutputS3BucketName    string
	OutputS3KeyPrefix     string
	MaxConcurrency        string
	MaxErrors             string
	TargetCount           int32
	CompletedCount        int32
	ErrorCount            int32
	DeliveryTimedOutCount int32
	ServiceRole           string
	Notification          *NotificationConfig
	CloudWatchOutput      *CloudWatchOutputConfig
	TimeoutSeconds        int32
}

// CommandPlugin is the result of one document step on one instance.
type CommandPlugin struct {
	Name                   string
	Status                 string
	StatusDetails          string
	ResponseCode           int32
	ResponseStartDateTime  time.Time
	ResponseFinishDateTime time.Time
	Output                 string
	StandardOutputURL      string
	StandardErrorURL       string
	OutputS3Region         string
	OutputS3BucketName     string
	OutputS3KeyPrefix      string
}

// CommandInvocation is a command's run on one instance. From
// GetCommandInvocation, PluginName, the timing, the response code and the
// output are those of the selected plugin (the first one when none is named).
type CommandInvocation struct {
	CommandID          string
	InstanceID         string
	InstanceName       string
	Comment            string
	DocumentName       string
	DocumentVersion    string
	RequestedDateTime  time.Time
	Status             string
	StatusDetails      string
	PluginName         string
	ResponseCode       int32
	ExecutionStartTime time.Time
	ExecutionEndTime   time.Time
	Stdout             string
	Stderr             string
	StandardOutputURL  string
	StandardErrorURL   string
	ServiceRole        string
	Notification       *NotificationConfig
	CloudWatchOutput   *CloudWatchOutputConfig
	Plugins            []CommandPlugin
}

// CommandFilter is one ListCommands or ListCommandInvocations filter. Keys are
// InvokedAfter, InvokedBefore, Status, ExecutionStage (ListCommands only) and
// DocumentName.
type CommandFilter struct {
	Key   string
	Value string
}

// CommandQuery selects commands or invocations. Every set field must match.
type CommandQuery struct {
	CommandID  string
	InstanceID string
	Filters    []CommandFilter
}

// RunCommand is an OPTIONAL capability, discovered by type assertion.
//
// Targets are validated: sending to an instance that does not exist, or that
// is not running, is InvalidInstanceId, as it is against the real service.
// Parameters are checked against the declared parameters of the document
// version that is sent.
//
// IMPORTANT: an emulated instance has no guest operating system, so nothing
// executes. Invocations report success and empty output. This exercises a
// caller's send/poll orchestration (that it waits for a terminal status, reads
// the response code, and handles failure) but it does NOT validate the script
// itself. A caller whose bootstrap script is wrong will still see success here.
type RunCommand interface {
	SendCommand(ctx context.Context, cfg CommandConfig) (*Command, error)
	// GetCommandInvocation reports one instance's run. pluginName selects a
	// step and may be empty.
	GetCommandInvocation(ctx context.Context, commandID, instanceID, pluginName string) (*CommandInvocation, error)
	// ListCommands returns the matching commands, newest first.
	ListCommands(ctx context.Context, q CommandQuery) ([]Command, error)
	// ListCommandInvocations returns the matching invocations, newest command
	// first. Plugins are filled only when details is set.
	ListCommandInvocations(ctx context.Context, q CommandQuery, details bool) ([]CommandInvocation, error)
	// CancelCommand cancels the command on the given instances, or on all of
	// them when instanceIDs is empty. Invocations that already finished keep
	// their result.
	CancelCommand(ctx context.Context, commandID string, instanceIDs []string) error
}

// InstanceInformation describes one managed node.
type InstanceInformation struct {
	InstanceID       string
	PingStatus       string
	LastPingDateTime time.Time
	AgentVersion     string
	IsLatestVersion  bool
	PlatformType     string
	PlatformName     string
	PlatformVersion  string
	ResourceType     string
	IPAddress        string
	ComputerName     string
	SourceID         string
	SourceType       string
}

// InstanceInformationFilter is one DescribeInstanceInformation filter. Keys
// are InstanceIds, PingStatus, PlatformType, ResourceType, AgentVersion,
// SourceIds, SourceTypes, tag-key and tag:<key>.
type InstanceInformationFilter struct {
	Key    string
	Values []string
}

// ManagedNodes is an OPTIONAL capability, discovered by type assertion. Every
// EC2 instance that is not terminated counts as a managed node: running ones
// are Online and the rest ConnectionLost.
type ManagedNodes interface {
	DescribeInstanceInformation(ctx context.Context, filters []InstanceInformationFilter) ([]InstanceInformation, error)
}
