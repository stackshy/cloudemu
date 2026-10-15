package driver

import "context"

// Search resource types (the ResourceType enum).
const (
	SearchTrainingJob              = "TrainingJob"
	SearchExperiment               = "Experiment"
	SearchExperimentTrial          = "ExperimentTrial"
	SearchExperimentTrialComponent = "ExperimentTrialComponent"
	SearchEndpoint                 = "Endpoint"
	SearchModel                    = "Model"
	SearchModelPackage             = "ModelPackage"
	SearchModelPackageGroup        = "ModelPackageGroup"
	SearchPipeline                 = "Pipeline"
	SearchPipelineExecution        = "PipelineExecution"
	SearchFeatureGroup             = "FeatureGroup"
	SearchFeatureMetadata          = "FeatureMetadata"
	SearchImage                    = "Image"
	SearchImageVersion             = "ImageVersion"
	SearchProject                  = "Project"
	SearchHyperParameterTuningJob  = "HyperParameterTuningJob"
	SearchModelCard                = "ModelCard"
	SearchPipelineVersion          = "PipelineVersion"
	SearchJob                      = "Job"
)

// Search limits.
const (
	MaxSearchListItems  = 20
	MaxSearchExpression = 50
	MaxSearchDepth      = 10
	MaxSuggestionHint   = 100
)

// SearchFilter is one property test of a search expression.
type SearchFilter struct {
	Name     string
	Operator string // Equals, NotEquals, Exists, NotExists, GreaterThan, ..., In, Contains
	Value    string
}

// SearchNested tests the items of a list property (for example
// InputDataConfig); an item must satisfy all of its filters.
type SearchNested struct {
	PropertyName string
	Filters      []SearchFilter
}

// SearchExpression combines filters, nested filters and sub-expressions with
// And (default) or Or.
type SearchExpression struct {
	Filters        []SearchFilter
	NestedFilters  []SearchNested
	SubExpressions []SearchExpression
	Operator       string
}

// SearchRequest describes a Search call.
type SearchRequest struct {
	Resource   string
	Expression *SearchExpression
	SortBy     string
	SortOrder  string
	MaxResults int
	NextToken  string
}

// SearchHit is one matching resource: Record holds its members under the SDK
// property names (times as RFC3339 strings).
type SearchHit struct {
	Resource string
	Record   map[string]any
}

// SearchResult is the outcome of Search.
type SearchResult struct {
	Hits      []SearchHit
	NextToken string
	TotalHits int
}

// searchAPI covers resource search.
type searchAPI interface {
	Search(ctx context.Context, req SearchRequest) (*SearchResult, error)
	GetSearchSuggestions(ctx context.Context, resource, propertyNameHint string) ([]string, error)
}
