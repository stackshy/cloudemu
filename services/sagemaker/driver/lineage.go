package driver

import "context"

// Lineage entity kinds.
const (
	LineageArtifact = "Artifact"
	LineageAction   = "Action"
	LineageContext  = "Context"
)

// Association edge types (AssociationEdgeType).
const (
	AssociationContributedTo  = "ContributedTo"
	AssociationAssociatedWith = "AssociatedWith"
	AssociationDerivedFrom    = "DerivedFrom"
	AssociationProduced       = "Produced"
	AssociationSameAs         = "SameAs"
)

// QueryLineage directions.
const (
	LineageBoth        = "Both"
	LineageAscendants  = "Ascendants"
	LineageDescendants = "Descendants"
)

// Lineage limits.
const (
	MaxLineageProperties    = 30
	MaxLineageQueryDepth    = 10
	MaxLineageQueryResults  = 50
	DefaultLineageGroupName = "sagemaker-default-lineage-group"
)

// LineageSourceType is one identifier of an artifact source (MD5Hash, S3ETag,
// S3Version or Custom).
type LineageSourceType struct {
	IDType string
	Value  string
}

// LineageSource is where a lineage entity came from.
type LineageSource struct {
	SourceURI   string
	SourceID    string
	SourceType  string
	SourceTypes []LineageSourceType // artifacts only
}

// LineageEntitySpec describes an artifact, action or context to create.
type LineageEntitySpec struct {
	Name        string
	Type        string
	Source      LineageSource
	Description string
	Status      string // actions only
	Properties  map[string]string
	Metadata    *MetadataProperties // artifacts and actions only
	Tags        []Tag
}

// LineageEntity is an artifact, action or context.
type LineageEntity struct {
	Kind             string
	ARN              string
	Name             string
	Type             string
	Source           LineageSource
	Description      string
	Status           string
	Properties       map[string]string
	Metadata         *MetadataProperties
	CreationTime     string
	LastModifiedTime string
}

// LineageEntityUpdate describes an update of an artifact (by ARN), action or
// context (by name). Properties, when non-nil, replace the existing set; keys in
// PropertiesToRemove are then dropped.
type LineageEntityUpdate struct {
	Key                string // ARN (artifact) or name (action, context)
	Name               *string
	Description        *string
	Status             *string
	Properties         map[string]string
	PropertiesToRemove []string
}

// Association is a directed edge between two lineage entities.
type Association struct {
	SourceARN      string
	DestinationARN string
	Type           string
	CreationTime   string
}

// AssociationSummary is an association with its endpoints resolved.
type AssociationSummary struct {
	Association
	SourceName      string
	DestinationName string
	SourceType      string
	DestinationType string
}

// LineageQuery describes a QueryLineage request.
type LineageQuery struct {
	StartARNs      []string
	Direction      string
	IncludeEdges   bool
	MaxDepth       int
	MaxResults     int
	NextToken      string
	LineageTypes   []string
	Types          []string
	Properties     map[string]string
	CreatedBefore  string
	CreatedAfter   string
	ModifiedBefore string
	ModifiedAfter  string
}

// LineageVertex is a node returned by QueryLineage.
type LineageVertex struct {
	ARN         string
	Type        string
	LineageType string
}

// LineageEdge is an edge returned by QueryLineage.
type LineageEdge struct {
	SourceARN       string
	DestinationARN  string
	AssociationType string
}

// LineageResult is the outcome of QueryLineage.
type LineageResult struct {
	Vertices  []LineageVertex
	Edges     []LineageEdge
	NextToken string
}

// LineageGroup is the account's lineage group.
type LineageGroup struct {
	Name             string
	ARN              string
	DisplayName      string
	CreationTime     string
	LastModifiedTime string
}

// lineageAPI covers ML lineage tracking: artifacts, actions, contexts,
// associations, lineage queries and the lineage group.
//
// ListArtifacts filters by opts.Filters keys ArtifactType and SourceUri;
// ListActions and ListContexts by ActionType / ContextType and SourceUri.
type lineageAPI interface {
	CreateArtifact(ctx context.Context, spec LineageEntitySpec) (*LineageEntity, error)
	DescribeArtifact(ctx context.Context, arn string) (*LineageEntity, error)
	ListArtifacts(ctx context.Context, opts ListOptions) (ListPage[LineageEntity], error)
	UpdateArtifact(ctx context.Context, update LineageEntityUpdate) (*LineageEntity, error)
	DeleteArtifact(ctx context.Context, arn, sourceURI string) (string, error)

	CreateAction(ctx context.Context, spec LineageEntitySpec) (*LineageEntity, error)
	DescribeAction(ctx context.Context, name string) (*LineageEntity, error)
	ListActions(ctx context.Context, opts ListOptions) (ListPage[LineageEntity], error)
	UpdateAction(ctx context.Context, update LineageEntityUpdate) (*LineageEntity, error)
	DeleteAction(ctx context.Context, name string) (string, error)

	CreateContext(ctx context.Context, spec LineageEntitySpec) (*LineageEntity, error)
	DescribeContext(ctx context.Context, name string) (*LineageEntity, error)
	ListContexts(ctx context.Context, opts ListOptions) (ListPage[LineageEntity], error)
	UpdateContext(ctx context.Context, update LineageEntityUpdate) (*LineageEntity, error)
	DeleteContext(ctx context.Context, name string) (string, error)

	AddAssociation(ctx context.Context, sourceARN, destinationARN, associationType string) error
	DeleteAssociation(ctx context.Context, sourceARN, destinationARN string) error
	// ListAssociations filters by opts.Filters keys AssociationType, SourceArn,
	// DestinationArn, SourceType and DestinationType.
	ListAssociations(ctx context.Context, opts ListOptions) (ListPage[AssociationSummary], error)
	QueryLineage(ctx context.Context, query LineageQuery) (*LineageResult, error)

	DescribeLineageGroup(ctx context.Context, nameOrARN string) (*LineageGroup, error)
	ListLineageGroups(ctx context.Context, opts ListOptions) (ListPage[LineageGroup], error)
	GetLineageGroupPolicy(ctx context.Context, nameOrARN string) (arn, policy string, err error)
}
