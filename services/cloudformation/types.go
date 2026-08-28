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
	StatusUpdateRollbackInProgress = "UPDATE_ROLLBACK_IN_PROGRESS"
	StatusUpdateRollbackComplete   = "UPDATE_ROLLBACK_COMPLETE"

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
	ResourceDeleteInProgress = "DELETE_IN_PROGRESS"
	ResourceDeleteComplete   = "DELETE_COMPLETE"
	ResourceDeleteFailed     = "DELETE_FAILED"
)

// Parameter is a name/value pair supplied to (or resolved for) a stack.
type Parameter struct {
	Key   string
	Value string
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

// CreateStackInput is the request to create a stack.
type CreateStackInput struct {
	StackName    string
	TemplateBody string
	Parameters   []Parameter
	Tags         map[string]string
	Capabilities []string
}

// UpdateStackInput is the request to update an existing stack.
type UpdateStackInput struct {
	StackName    string
	TemplateBody string
	Parameters   []Parameter
	Tags         map[string]string
	Capabilities []string
}

// API is the CloudFormation control surface a wire handler drives. The AWS
// stack-store mock (providers/aws/cloudformation) implements it; keeping it here
// lets the server layer depend on the service package rather than the provider.
type API interface {
	CreateStack(ctx context.Context, in *CreateStackInput) (*Stack, error)
	UpdateStack(ctx context.Context, in *UpdateStackInput) (*Stack, error)
	DeleteStack(ctx context.Context, stackName string) error
	DescribeStacks(ctx context.Context, stackName string) ([]Stack, error)
	DescribeStackEvents(ctx context.Context, stackName string) ([]StackEvent, error)
	ListStacks(ctx context.Context, statusFilter []string) ([]StackSummary, error)
	DescribeStackResources(ctx context.Context, stackName string) ([]StackResource, error)
	ListStackResources(ctx context.Context, stackName string) ([]StackResource, error)
	GetTemplate(ctx context.Context, stackName string) (string, error)
}
