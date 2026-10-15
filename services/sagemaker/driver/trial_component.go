package driver

import "context"

// Trial component primary status values.
const (
	TrialComponentInProgress = "InProgress"
	TrialComponentCompleted  = "Completed"
	TrialComponentFailed     = "Failed"
	TrialComponentStopping   = "Stopping"
	TrialComponentStopped    = "Stopped"
)

// Trial component limits.
const (
	MaxTrialComponentParameters = 300
	MaxTrialComponentArtifacts  = 60
)

// MetadataProperties describe where an entity came from (commit, repository).
type MetadataProperties struct {
	CommitID    string
	GeneratedBy string
	ProjectID   string
	Repository  string
}

// TrialComponentStatus is a trial component's status.
type TrialComponentStatus struct {
	PrimaryStatus string
	Message       string
}

// TrialComponentParameter is a parameter value: exactly one of the two is set.
type TrialComponentParameter struct {
	StringValue *string
	NumberValue *float64
}

// TrialComponentArtifact is an input or output artifact of a trial component.
type TrialComponentArtifact struct {
	Value     string
	MediaType string
}

// TrialComponentSpec describes a trial component to create.
type TrialComponentSpec struct {
	Name            string
	DisplayName     string
	StartTime       string
	EndTime         string
	Status          *TrialComponentStatus
	Parameters      map[string]TrialComponentParameter
	InputArtifacts  map[string]TrialComponentArtifact
	OutputArtifacts map[string]TrialComponentArtifact
	Metadata        *MetadataProperties
	Tags            []Tag
}

// TrialComponent is one stage of an experiment trial.
type TrialComponent struct {
	Name             string
	ARN              string
	DisplayName      string
	StartTime        string
	EndTime          string
	Status           *TrialComponentStatus
	Parameters       map[string]TrialComponentParameter
	InputArtifacts   map[string]TrialComponentArtifact
	OutputArtifacts  map[string]TrialComponentArtifact
	Metadata         *MetadataProperties
	SourceARN        string
	SourceType       string
	CreationTime     string
	LastModifiedTime string
	// TrialNames lists the trials this component is associated with (sorted).
	TrialNames []string
}

// TrialComponentUpdate describes an UpdateTrialComponent request; parameters
// and artifacts given are merged into the existing ones by key.
type TrialComponentUpdate struct {
	Name                    string
	DisplayName             *string
	StartTime               *string
	EndTime                 *string
	Status                  *TrialComponentStatus
	Parameters              map[string]TrialComponentParameter
	ParametersToRemove      []string
	InputArtifacts          map[string]TrialComponentArtifact
	InputArtifactsToRemove  []string
	OutputArtifacts         map[string]TrialComponentArtifact
	OutputArtifactsToRemove []string
}

// trialComponentAPI covers experiment trial components.
type trialComponentAPI interface {
	CreateTrialComponent(ctx context.Context, spec TrialComponentSpec) (*TrialComponent, error)
	DescribeTrialComponent(ctx context.Context, name string) (*TrialComponent, error)
	// ListTrialComponents filters by opts.Filters keys ExperimentName, TrialName
	// and SourceArn, which are mutually exclusive.
	ListTrialComponents(ctx context.Context, opts ListOptions) (ListPage[TrialComponent], error)
	UpdateTrialComponent(ctx context.Context, update TrialComponentUpdate) (*TrialComponent, error)
	DeleteTrialComponent(ctx context.Context, name string) (*TrialComponent, error)
	AssociateTrialComponent(ctx context.Context, componentName, trialName string) (componentARN, trialARN string, err error)
	DisassociateTrialComponent(ctx context.Context, componentName, trialName string) (componentARN, trialARN string, err error)
}
