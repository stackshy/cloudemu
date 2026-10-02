package cloudformation

import (
	"context"
	"time"
)

// Stack status values, a subset of the real CloudFormation set covering the
// lifecycle the synchronous emulator models.
const (
	StatusCreateInProgress   = "CREATE_IN_PROGRESS"
	StatusCreateComplete     = "CREATE_COMPLETE"
	StatusCreateFailed       = "CREATE_FAILED"
	StatusRollbackInProgress = "ROLLBACK_IN_PROGRESS"
	StatusRollbackComplete   = "ROLLBACK_COMPLETE"
	StatusRollbackFailed     = "ROLLBACK_FAILED"

	StatusUpdateInProgress         = "UPDATE_IN_PROGRESS"
	StatusUpdateComplete           = "UPDATE_COMPLETE"
	StatusUpdateFailed             = "UPDATE_FAILED"
	StatusUpdateRollbackInProgress = "UPDATE_ROLLBACK_IN_PROGRESS"
	StatusUpdateRollbackComplete   = "UPDATE_ROLLBACK_COMPLETE"
	StatusUpdateRollbackFailed     = "UPDATE_ROLLBACK_FAILED"

	StatusUpdateCompleteCleanupInProgress         = "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS"
	StatusUpdateRollbackCompleteCleanupInProgress = "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS"

	StatusDeleteInProgress = "DELETE_IN_PROGRESS"
	StatusDeleteComplete   = "DELETE_COMPLETE"
	StatusDeleteFailed     = "DELETE_FAILED"
)

// Resource status values recorded on each stack resource and its events.
const (
	ResourceCreateInProgress = "CREATE_IN_PROGRESS"
	ResourceCreateComplete   = "CREATE_COMPLETE"
	ResourceCreateFailed     = "CREATE_FAILED"
	ResourceUpdateInProgress = "UPDATE_IN_PROGRESS"
	ResourceUpdateComplete   = "UPDATE_COMPLETE"
	ResourceUpdateFailed     = "UPDATE_FAILED"
	ResourceDeleteInProgress = "DELETE_IN_PROGRESS"
	ResourceDeleteComplete   = "DELETE_COMPLETE"
	ResourceDeleteFailed     = "DELETE_FAILED"
	ResourceDeleteSkipped    = "DELETE_SKIPPED"
)

// DeleteStack DeletionMode values.
const (
	DeletionModeStandard    = "STANDARD"
	DeletionModeForceDelete = "FORCE_DELETE_STACK"
)

// Parameter is a name/value pair supplied to (or resolved for) a stack.
type Parameter struct {
	Key   string
	Value string
	// NoEcho marks a value that stack reads show masked.
	NoEcho bool
	// ResolvedValue is the Parameter Store value an SSM parameter type
	// resolved to. It is empty for other types.
	ResolvedValue string
	// UsePreviousValue asks UpdateStack to keep the stack's current value.
	// It is only read from requests and never stored.
	UsePreviousValue bool `json:"-"`
}

// Output is a resolved stack output.
type Output struct {
	Key         string
	Value       string
	Description string
	ExportName  string
}

// StackResource is one logical resource of a stack and its current physical
// mapping.
type StackResource struct {
	LogicalID    string
	PhysicalID   string
	Type         string
	Status       string
	StatusReason string
	Timestamp    time.Time
}

// StackEvent records one step of a stack operation, mirroring the CloudFormation
// event stream a client polls during a deploy.
type StackEvent struct {
	EventID      string
	StackID      string
	StackName    string
	LogicalID    string
	PhysicalID   string
	ResourceType string
	Status       string
	StatusReason string
	Timestamp    time.Time
}

// Stack is the full state of a deployed stack.
type Stack struct {
	ID           string
	Name         string
	Status       string
	StatusReason string
	Description  string
	Parameters   []Parameter
	Outputs      []Output
	Tags         map[string]string
	Capabilities []string
	TemplateBody string
	CreationTime time.Time
	LastUpdated  time.Time
	DeletionTime time.Time
	Resources    []StackResource
	Events       []StackEvent

	// NotificationARNs are the SNS topics the stack reports events to.
	NotificationARNs []string
	// ChangeSetID is the change set the stack was last created or updated
	// from.
	ChangeSetID string
	// DisableRollback records that a failed operation leaves the stack as it
	// is instead of rolling it back.
	DisableRollback bool
	// EnableTerminationProtection blocks DeleteStack while it is set.
	EnableTerminationProtection bool
	// RetainExceptOnCreate has the rollback of the last operation delete the
	// resources it created, even those whose DeletionPolicy is Retain.
	RetainExceptOnCreate bool
	// DeletionMode is the mode of the last DeleteStack call.
	DeletionMode string
}

// StackSummary is the condensed stack view ListStacks returns.
type StackSummary struct {
	ID                  string
	Name                string
	Status              string
	StatusReason        string
	TemplateDescription string
	CreationTime        time.Time
	LastUpdated         time.Time
	DeletionTime        time.Time
}

// CreateStackInput is the request to create a stack. Exactly one of
// TemplateBody and TemplateURL is set.
type CreateStackInput struct {
	StackName    string
	TemplateBody string
	TemplateURL  string
	Parameters   []Parameter
	Tags         map[string]string
	Capabilities []string

	NotificationARNs []string
	// OnFailure is ROLLBACK (the default), DO_NOTHING or DELETE. It cannot
	// be combined with DisableRollback, which means DO_NOTHING.
	OnFailure       string
	DisableRollback bool
	// EnableTerminationProtection protects the new stack from DeleteStack.
	EnableTerminationProtection bool
	// RetainExceptOnCreate deletes the created resources on a rollback,
	// even those whose DeletionPolicy is Retain.
	RetainExceptOnCreate bool
}

// DeleteStackInput is the request to delete a stack.
type DeleteStackInput struct {
	StackName string
	// RetainResources names resources to leave in place. It is valid only
	// for a stack in DELETE_FAILED.
	RetainResources []string
	// DeletionMode is STANDARD (the default) or FORCE_DELETE_STACK, which
	// deletes a DELETE_FAILED stack and keeps the resources it cannot
	// delete.
	DeletionMode string
}

// UpdateTerminationProtectionInput turns a stack's termination protection
// on or off.
type UpdateTerminationProtectionInput struct {
	StackName string
	Enable    bool
}

// Export is one exported output value.
type Export struct {
	ExportingStackID string
	Name             string
	Value            string
}

// ExportList is one page of ListExports.
type ExportList struct {
	Exports   []Export
	NextToken string
}

// ListImportsInput names the export whose importing stacks to list.
type ListImportsInput struct {
	ExportName string
	NextToken  string
}

// ImportList is one page of ListImports: the names of the stacks that
// import the export.
type ImportList struct {
	Imports   []string
	NextToken string
}

// AccountLimit is one CloudFormation quota of the account.
type AccountLimit struct {
	Name  string
	Value int
}

// EstimateTemplateCostInput names the template to price.
type EstimateTemplateCostInput struct {
	TemplateBody string
	TemplateURL  string
	Parameters   []Parameter
}

// UpdateStackInput is the request to update an existing stack.
type UpdateStackInput struct {
	StackName    string
	TemplateBody string
	TemplateURL  string
	Parameters   []Parameter
	Tags         map[string]string
	Capabilities []string
	// UsePreviousTemplate reuses the stack's current template.
	UsePreviousTemplate bool
	// DisableRollback leaves a failed update UPDATE_FAILED instead of
	// rolling it back.
	DisableRollback bool
	// RetainExceptOnCreate deletes the resources the update created when it
	// rolls back, even those whose DeletionPolicy is Retain.
	RetainExceptOnCreate bool

	// NotificationARNs replaces the stack's topics. Nil keeps them.
	NotificationARNs []string
}

// ContinueUpdateRollbackInput is the request to retry the rollback of a stack
// in UPDATE_ROLLBACK_FAILED.
type ContinueUpdateRollbackInput struct {
	StackName string
	// ResourcesToSkip names failed resources the rollback leaves as they are.
	ResourcesToSkip []string
}

// ValidateTemplateInput is the request to validate a template. TemplateBody
// wins when both fields are set.
type ValidateTemplateInput struct {
	TemplateBody string
	TemplateURL  string
}

// TemplateParameter is one parameter declaration as ValidateTemplate reports it.
type TemplateParameter struct {
	Key          string
	Type         string
	DefaultValue string
	HasDefault   bool
	NoEcho       bool
	Description  string
}

// TemplateSummary is the ValidateTemplate and GetTemplateSummary result.
type TemplateSummary struct {
	Description        string
	Parameters         []TemplateParameter
	Capabilities       []string
	CapabilitiesReason string
	DeclaredTransforms []string
	// ResourceTypes and Version are reported by GetTemplateSummary only.
	ResourceTypes []string
	Version       string
}

// GetTemplateSummaryInput names the template to summarize: a live stack's,
// or one given as a body or URL.
type GetTemplateSummaryInput struct {
	StackName    string
	TemplateBody string
	TemplateURL  string
}

// API is the CloudFormation control surface a wire handler drives. The AWS
// stack-store mock (providers/aws/cloudformation) implements it; keeping it here
// lets the server layer depend on the service package rather than the provider.
type API interface {
	CreateStack(ctx context.Context, in *CreateStackInput) (*Stack, error)
	UpdateStack(ctx context.Context, in *UpdateStackInput) (*Stack, error)
	ContinueUpdateRollback(ctx context.Context, in *ContinueUpdateRollbackInput) error
	DeleteStack(ctx context.Context, in *DeleteStackInput) error
	DescribeStacks(ctx context.Context, stackName string) ([]Stack, error)
	DescribeStackEvents(ctx context.Context, stackName string) ([]StackEvent, error)
	ListStacks(ctx context.Context, statusFilter []string) ([]StackSummary, error)
	DescribeStackResources(ctx context.Context, stackName string) ([]StackResource, error)
	ListStackResources(ctx context.Context, stackName string) ([]StackResource, error)
	GetTemplate(ctx context.Context, stackName string) (string, error)
	ValidateTemplate(ctx context.Context, in *ValidateTemplateInput) (*TemplateSummary, error)
	GetTemplateSummary(ctx context.Context, in *GetTemplateSummaryInput) (*TemplateSummary, error)

	CreateChangeSet(ctx context.Context, in *CreateChangeSetInput) (*ChangeSet, error)
	DescribeChangeSet(ctx context.Context, in *DescribeChangeSetInput) (*ChangeSet, error)
	ListChangeSets(ctx context.Context, in *ListChangeSetsInput) (*ChangeSetList, error)
	ExecuteChangeSet(ctx context.Context, in *ExecuteChangeSetInput) error
	DeleteChangeSet(ctx context.Context, in *DeleteChangeSetInput) error

	ListExports(ctx context.Context, nextToken string) (*ExportList, error)
	ListImports(ctx context.Context, in *ListImportsInput) (*ImportList, error)
	UpdateTerminationProtection(ctx context.Context, in *UpdateTerminationProtectionInput) (string, error)
	DescribeAccountLimits(ctx context.Context, nextToken string) ([]AccountLimit, error)
	EstimateTemplateCost(ctx context.Context, in *EstimateTemplateCostInput) (string, error)
}
