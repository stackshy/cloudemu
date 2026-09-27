package cloudformation

import "time"

// StatusReviewInProgress is the status of a stack that a CREATE change set
// made and that has not been executed yet.
const StatusReviewInProgress = "REVIEW_IN_PROGRESS"

// Change set types.
const (
	ChangeSetTypeCreate = "CREATE"
	ChangeSetTypeUpdate = "UPDATE"
	ChangeSetTypeImport = "IMPORT"
)

// Change set Status values. Change sets are planned synchronously, so a new
// one is already CREATE_COMPLETE or FAILED when CreateChangeSet returns.
const (
	ChangeSetStatusCreateComplete = "CREATE_COMPLETE"
	ChangeSetStatusFailed         = "FAILED"
)

// Change set ExecutionStatus values.
const (
	ExecutionUnavailable = "UNAVAILABLE"
	ExecutionAvailable   = "AVAILABLE"
	ExecutionInProgress  = "EXECUTE_IN_PROGRESS"
	ExecutionComplete    = "EXECUTE_COMPLETE"
	ExecutionFailed      = "EXECUTE_FAILED"
	ExecutionObsolete    = "OBSOLETE"
)

// OnStackFailure values: what executing a change set does when the stack
// operation fails.
const (
	OnStackFailureRollback  = "ROLLBACK"
	OnStackFailureDoNothing = "DO_NOTHING"
	OnStackFailureDelete    = "DELETE"
)

// Change actions. A property value change also reports one of these as its
// AttributeChangeType.
const (
	ChangeActionAdd    = "Add"
	ChangeActionModify = "Modify"
	ChangeActionRemove = "Remove"
)

// Replacement values of a Modify change.
const (
	ReplacementTrue        = "True"
	ReplacementFalse       = "False"
	ReplacementConditional = "Conditional"
)

// RequiresRecreation values of a change target.
const (
	RecreationNever  = "Never"
	RecreationAlways = "Always"
)

// Resource attributes a change can target.
const (
	AttributeProperties = "Properties"
	AttributeTags       = "Tags"
)

// Change sources: the kind of entity that caused a change.
const (
	SourceDirectModification = "DirectModification"
	SourceParameterReference = "ParameterReference"
	SourceResourceReference  = "ResourceReference"
	SourceResourceAttribute  = "ResourceAttribute"
)

// Evaluation types of a change detail.
const (
	EvaluationStatic  = "Static"
	EvaluationDynamic = "Dynamic"
)

// Policy actions taken on the physical resource of a change.
const (
	PolicyDelete           = "Delete"
	PolicyReplaceAndDelete = "ReplaceAndDelete"
)

// Exception names the change set operations report.
const (
	ExceptionAlreadyExists          = "AlreadyExistsException"
	ExceptionChangeSetNotFound      = "ChangeSetNotFound"
	ExceptionInvalidChangeSetStatus = "InvalidChangeSetStatus"
)

// ChangeTarget is the part of a resource a change detail touches. The value
// fields are filled only when a caller asks for property values.
type ChangeTarget struct {
	Attribute          string
	Name               string
	RequiresRecreation string

	BeforeValue         string
	AfterValue          string
	AttributeChangeType string
}

// ChangeDetail is one reason a resource changes.
type ChangeDetail struct {
	Target        ChangeTarget
	Evaluation    string
	ChangeSource  string
	CausingEntity string
}

// ResourceChange is what executing a change set does to one resource.
type ResourceChange struct {
	Action       string
	LogicalID    string
	PhysicalID   string
	ResourceType string
	Replacement  string
	PolicyAction string
	Scope        []string
	Details      []ChangeDetail
	// BeforeContext and AfterContext hold the resource's properties as JSON.
	// They are returned only when a caller asks for property values.
	BeforeContext string
	AfterContext  string
}

// ChangeSet is a stored change set as DescribeChangeSet and ListChangeSets
// report it.
type ChangeSet struct {
	ID               string
	Name             string
	StackID          string
	StackName        string
	Type             string
	Description      string
	Status           string
	StatusReason     string
	ExecutionStatus  string
	CreationTime     time.Time
	Parameters       []Parameter
	Tags             map[string]string
	Capabilities     []string
	NotificationARNs []string
	OnStackFailure   string
	Changes          []ResourceChange
	// NextToken is set on a DescribeChangeSet page that has more changes.
	NextToken string `json:"-"`
}

// CreateChangeSetInput is the request to create a change set. ChangeSetType
// defaults to UPDATE.
type CreateChangeSetInput struct {
	StackName           string
	ChangeSetName       string
	ChangeSetType       string
	Description         string
	TemplateBody        string
	TemplateURL         string
	UsePreviousTemplate bool
	Parameters          []Parameter
	Tags                map[string]string
	Capabilities        []string
	NotificationARNs    []string
	OnStackFailure      string
	// ClientToken makes a retry of the same request return the change set it
	// made instead of failing on the duplicate name.
	ClientToken string
}

// DescribeChangeSetInput names a change set by ARN, or by name within
// StackName.
type DescribeChangeSetInput struct {
	ChangeSetName         string
	StackName             string
	NextToken             string
	IncludePropertyValues bool
}

// ListChangeSetsInput is the request to list a stack's change sets.
type ListChangeSetsInput struct {
	StackName string
	NextToken string
}

// ChangeSetList is one page of change set summaries. The summaries carry no
// Changes.
type ChangeSetList struct {
	Summaries []ChangeSet
	NextToken string
}

// ExecuteChangeSetInput is the request to execute a change set.
// DisableRollback is nil when the request leaves it out.
type ExecuteChangeSetInput struct {
	ChangeSetName   string
	StackName       string
	DisableRollback *bool
	// ClientRequestToken makes a retry of an execution that already started
	// succeed without running it again.
	ClientRequestToken string
}

// DeleteChangeSetInput is the request to delete a change set.
type DeleteChangeSetInput struct {
	ChangeSetName string
	StackName     string
}
