package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Featured results set status values.
const (
	FeaturedActive   = "ACTIVE"
	FeaturedInactive = "INACTIVE"
)

// Principal mapping status values reported per ordering id.
const (
	MappingProcessing = "PROCESSING"
	MappingSucceeded  = "SUCCEEDED"
	MappingDeleting   = "DELETING"
	MappingDeleted    = "DELETED"
	MappingFailed     = "FAILED"
)

// Query suggestions modes and statuses.
const (
	SuggestionsEnabled   = "ENABLED"
	SuggestionsLearnOnly = "LEARN_ONLY"
	SuggestionsActive    = "ACTIVE"
	SuggestionsUpdating  = "UPDATING"
)

// FeaturedResultsSet maps query texts to documents featured above the results.
type FeaturedResultsSet struct {
	ID                string
	IndexID           string
	Name              string
	Description       string
	Status            string
	QueryTexts        []string
	FeaturedDocuments []string
	ClientToken       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Tags              []Tag
}

// FeaturedDocumentRef is a featured document that exists in the index, with the
// metadata DescribeFeaturedResultsSet reports for it.
type FeaturedDocumentRef struct {
	ID    string
	Title string
	URI   string
}

// FeaturedResultsSetView is a featured results set with its documents resolved
// against the index.
type FeaturedResultsSetView struct {
	FeaturedResultsSet
	DocumentsWithMetadata []FeaturedDocumentRef
	DocumentsMissing      []string
}

// CreateFeaturedResultsSetInput is the input to CreateFeaturedResultsSet.
type CreateFeaturedResultsSetInput struct {
	IndexID           string
	Name              string
	Description       string
	Status            string
	QueryTexts        []string
	FeaturedDocuments []string
	ClientToken       string
	Tags              []Tag
}

// UpdateFeaturedResultsSetInput is the input to UpdateFeaturedResultsSet; a nil
// member is left unchanged.
type UpdateFeaturedResultsSetInput struct {
	IndexID           string
	ID                string
	Name              *string
	Description       *string
	Status            *string
	QueryTexts        []string
	QueryTextsSet     bool
	FeaturedDocuments []string
	DocumentsSet      bool
}

// BatchDeleteError reports one featured results set that could not be deleted.
type BatchDeleteError struct {
	ID           string
	ErrorCode    string
	ErrorMessage string
}

// FeaturedResults is the optional featured results capability.
type FeaturedResults interface {
	CreateFeaturedResultsSet(ctx context.Context, in *CreateFeaturedResultsSetInput) (*FeaturedResultsSet, error)
	DescribeFeaturedResultsSet(ctx context.Context, indexID, id string) (*FeaturedResultsSetView, error)
	UpdateFeaturedResultsSet(ctx context.Context, in *UpdateFeaturedResultsSetInput) (*FeaturedResultsSet, error)
	ListFeaturedResultsSets(ctx context.Context, indexID string, page Page) (sets []FeaturedResultsSet, nextToken string, err error)
	BatchDeleteFeaturedResultsSet(ctx context.Context, indexID string, ids []string) ([]BatchDeleteError, error)
}

// OrderingSummary is the processing state of one PutPrincipalMapping /
// DeletePrincipalMapping action for a group.
type OrderingSummary struct {
	OrderingID    int64
	Status        string
	ReceivedAt    time.Time
	LastUpdatedAt time.Time
	FailureReason string
}

// PutPrincipalMappingInput is the input to PutPrincipalMapping. GroupMembers
// round-trips as raw JSON.
type PutPrincipalMappingInput struct {
	IndexID      string
	DataSourceID string
	GroupID      string
	GroupMembers json.RawMessage
	OrderingID   *int64
	RoleArn      string
}

// DeletePrincipalMappingInput is the input to DeletePrincipalMapping.
type DeletePrincipalMappingInput struct {
	IndexID      string
	DataSourceID string
	GroupID      string
	OrderingID   *int64
}

// PrincipalMappingDescription is the DescribePrincipalMapping result.
type PrincipalMappingDescription struct {
	IndexID      string
	DataSourceID string
	GroupID      string
	Summaries    []OrderingSummary
}

// GroupSummary is a group mapped before a given ordering id.
type GroupSummary struct {
	GroupID    string
	OrderingID int64
}

// ListGroupsInput is the input to ListGroupsOlderThanOrderingId.
type ListGroupsInput struct {
	IndexID      string
	DataSourceID string
	OrderingID   int64
	Page         Page
}

// PrincipalMappings is the optional user-to-group principal mapping capability.
type PrincipalMappings interface {
	PutPrincipalMapping(ctx context.Context, in *PutPrincipalMappingInput) error
	DeletePrincipalMapping(ctx context.Context, in *DeletePrincipalMappingInput) error
	DescribePrincipalMapping(ctx context.Context, indexID, dataSourceID, groupID string) (*PrincipalMappingDescription, error)
	ListGroupsOlderThanOrderingID(ctx context.Context, in *ListGroupsInput) (groups []GroupSummary, nextToken string, err error)
}

// SuggestionsConfig is an index's query suggestions settings. The attribute
// suggestions block round-trips as raw JSON.
type SuggestionsConfig struct {
	Mode                                 string
	Status                               string
	QueryLogLookBackWindowInDays         int32
	IncludeQueriesWithoutUserInformation bool
	MinimumNumberOfQueryingUsers         int32
	MinimumQueryCount                    int32
	AttributeSuggestionsConfig           json.RawMessage
	LastSuggestionsBuildTime             time.Time
	LastClearTime                        time.Time
	TotalSuggestionsCount                int32
}

// UpdateSuggestionsConfigInput is the input to UpdateQuerySuggestionsConfig; a
// nil member is left unchanged.
type UpdateSuggestionsConfigInput struct {
	IndexID                              string
	Mode                                 *string
	QueryLogLookBackWindowInDays         *int32
	IncludeQueriesWithoutUserInformation *bool
	MinimumNumberOfQueryingUsers         *int32
	MinimumQueryCount                    *int32
	AttributeSuggestionsConfig           json.RawMessage
}

// GetSuggestionsInput is the input to GetQuerySuggestions.
type GetSuggestionsInput struct {
	IndexID             string
	QueryText           string
	MaxSuggestionsCount int32
	SuggestionTypes     []string
}

// Suggestion is one suggested query.
type Suggestion struct {
	ID         string
	Text       string
	Highlights []Highlight
}

// SuggestionsResult is the GetQuerySuggestions result.
type SuggestionsResult struct {
	QuerySuggestionsID string
	Suggestions        []Suggestion
}

// QuerySuggestions is the optional query suggestions capability.
type QuerySuggestions interface {
	DescribeQuerySuggestionsConfig(ctx context.Context, indexID string) (*SuggestionsConfig, error)
	UpdateQuerySuggestionsConfig(ctx context.Context, in *UpdateSuggestionsConfigInput) error
	ClearQuerySuggestions(ctx context.Context, indexID string) error
	GetQuerySuggestions(ctx context.Context, in *GetSuggestionsInput) (*SuggestionsResult, error)
}
