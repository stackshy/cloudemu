package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Sync job status values. A job is SYNCING while it runs and ends SUCCEEDED, or
// ABORTED when it is stopped.
const (
	SyncStatusSyncing    = "SYNCING"
	SyncStatusSucceeded  = "SUCCEEDED"
	SyncStatusAborted    = "ABORTED"
	SyncStatusStopping   = "STOPPING"
	SyncStatusFailed     = "FAILED"
	SyncStatusIncomplete = "INCOMPLETE"
	SyncStatusIndexing   = "SYNCING_INDEXING"
)

// Status values shared by the index child resources (FAQ, thesaurus, experience,
// query suggestions block list). A resource is created ACTIVE unless the server
// runs with async settling, when it reports CREATING first.
const (
	ChildStatusCreating = "CREATING"
	ChildStatusActive   = "ACTIVE"
	ChildStatusUpdating = "UPDATING"
	ChildStatusDeleting = "DELETING"
	ChildStatusFailed   = "FAILED"
)

// SyncJobMetrics are the document counters of a sync job (strings on the wire).
type SyncJobMetrics struct {
	DocumentsAdded    string
	DocumentsDeleted  string
	DocumentsFailed   string
	DocumentsModified string
	DocumentsScanned  string
}

// SyncJob is one data source synchronization run.
type SyncJob struct {
	ExecutionID         string
	StartTime           time.Time
	EndTime             time.Time
	Status              string
	ErrorMessage        string
	ErrorCode           string
	DataSourceErrorCode string
	Metrics             SyncJobMetrics
}

// ListSyncJobsInput filters a data source's sync job history.
type ListSyncJobsInput struct {
	IndexID      string
	DataSourceID string
	Page         Page
	StartTime    *time.Time
	EndTime      *time.Time
	StatusFilter string
}

// SyncJobs is the optional data source synchronization capability.
type SyncJobs interface {
	StartDataSourceSyncJob(ctx context.Context, indexID, dataSourceID string) (executionID string, err error)
	StopDataSourceSyncJob(ctx context.Context, indexID, dataSourceID string) error
	ListDataSourceSyncJobs(ctx context.Context, in *ListSyncJobsInput) (jobs []SyncJob, nextToken string, err error)
}

// Faq is a question-and-answer file registered with an index. The S3 object is
// not read: the emulator records the reference only.
type Faq struct {
	ID           string
	IndexID      string
	Name         string
	Description  string
	RoleArn      string
	S3Path       S3Path
	FileFormat   string
	LanguageCode string
	Status       string
	ErrorMessage string
	ClientToken  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Tags         []Tag
}

// CreateFaqInput is the input to CreateFaq.
type CreateFaqInput struct {
	IndexID      string
	Name         string
	Description  string
	RoleArn      string
	S3Path       S3Path
	FileFormat   string
	LanguageCode string
	ClientToken  string
	Tags         []Tag
}

// Faqs is the optional FAQ capability (Kendra has no UpdateFaq operation).
type Faqs interface {
	CreateFaq(ctx context.Context, in *CreateFaqInput) (*Faq, error)
	DescribeFaq(ctx context.Context, indexID, id string) (*Faq, error)
	ListFaqs(ctx context.Context, indexID string, page Page) (faqs []Faq, nextToken string, err error)
	DeleteFaq(ctx context.Context, indexID, id string) error
}

// SourceFile is what a thesaurus and a query suggestions block list share: a file
// in S3 registered with an index. The emulator records the reference only (the
// object is not read), so FileSizeBytes and the derived counts are 0.
type SourceFile struct {
	ID            string
	IndexID       string
	Name          string
	Description   string
	RoleArn       string
	SourceS3Path  S3Path
	Status        string
	ErrorMessage  string
	FileSizeBytes int64
	ClientToken   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Tags          []Tag
}

// Thesaurus is a synonym file registered with an index.
type Thesaurus struct {
	SourceFile
	TermCount        int64
	SynonymRuleCount int64
}

// CreateThesaurusInput is the input to CreateThesaurus.
type CreateThesaurusInput struct {
	IndexID      string
	Name         string
	Description  string
	RoleArn      string
	SourceS3Path S3Path
	ClientToken  string
	Tags         []Tag
}

// UpdateThesaurusInput is the input to UpdateThesaurus; a nil member is left
// unchanged.
type UpdateThesaurusInput struct {
	IndexID      string
	ID           string
	Name         *string
	Description  *string
	RoleArn      *string
	SourceS3Path *S3Path
}

// Thesauri is the optional thesaurus capability.
//
//nolint:dupl // the capabilities share one CRUD shape by design
type Thesauri interface {
	CreateThesaurus(ctx context.Context, in *CreateThesaurusInput) (*Thesaurus, error)
	DescribeThesaurus(ctx context.Context, indexID, id string) (*Thesaurus, error)
	UpdateThesaurus(ctx context.Context, in *UpdateThesaurusInput) error
	ListThesauri(ctx context.Context, indexID string, page Page) (thesauri []Thesaurus, nextToken string, err error)
	DeleteThesaurus(ctx context.Context, indexID, id string) error
}

// BlockList is a query suggestions block list registered with an index.
type BlockList struct {
	SourceFile
	ItemCount int32
}

// CreateBlockListInput is the input to CreateQuerySuggestionsBlockList.
type CreateBlockListInput struct {
	IndexID      string
	Name         string
	Description  string
	RoleArn      string
	SourceS3Path S3Path
	ClientToken  string
	Tags         []Tag
}

// UpdateBlockListInput is the input to UpdateQuerySuggestionsBlockList.
type UpdateBlockListInput struct {
	IndexID      string
	ID           string
	Name         *string
	Description  *string
	RoleArn      *string
	SourceS3Path *S3Path
}

// BlockLists is the optional query suggestions block list capability.
//
//nolint:dupl // the capabilities share one CRUD shape by design
type BlockLists interface {
	CreateQuerySuggestionsBlockList(ctx context.Context, in *CreateBlockListInput) (*BlockList, error)
	DescribeQuerySuggestionsBlockList(ctx context.Context, indexID, id string) (*BlockList, error)
	UpdateQuerySuggestionsBlockList(ctx context.Context, in *UpdateBlockListInput) error
	ListQuerySuggestionsBlockLists(ctx context.Context, indexID string, page Page) (lists []BlockList, nextToken string, err error)
	DeleteQuerySuggestionsBlockList(ctx context.Context, indexID, id string) error
}

// Experience is a search experience (a hosted search UI) of an index. The
// Configuration block round-trips as raw JSON.
type Experience struct {
	ID            string
	IndexID       string
	Name          string
	Description   string
	RoleArn       string
	Configuration json.RawMessage
	Status        string
	ErrorMessage  string
	Endpoints     []ExperienceEndpoint
	ClientToken   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ExperienceEndpoint is a URL an experience is served at.
type ExperienceEndpoint struct {
	Endpoint     string
	EndpointType string
}

// CreateExperienceInput is the input to CreateExperience.
type CreateExperienceInput struct {
	IndexID       string
	Name          string
	Description   string
	RoleArn       string
	Configuration json.RawMessage
	ClientToken   string
}

// UpdateExperienceInput is the input to UpdateExperience.
type UpdateExperienceInput struct {
	IndexID       string
	ID            string
	Name          *string
	Description   *string
	RoleArn       *string
	Configuration json.RawMessage
}

// Experiences is the optional search experience capability.
//
//nolint:dupl // the capabilities share one CRUD shape by design
type Experiences interface {
	CreateExperience(ctx context.Context, in *CreateExperienceInput) (*Experience, error)
	DescribeExperience(ctx context.Context, indexID, id string) (*Experience, error)
	UpdateExperience(ctx context.Context, in *UpdateExperienceInput) error
	ListExperiences(ctx context.Context, indexID string, page Page) (experiences []Experience, nextToken string, err error)
	DeleteExperience(ctx context.Context, indexID, id string) error
}

// AccessControlConfiguration is a reusable document access control list.
type AccessControlConfiguration struct {
	ID                            string
	IndexID                       string
	Name                          string
	Description                   string
	AccessControlList             json.RawMessage
	HierarchicalAccessControlList json.RawMessage
	ErrorMessage                  string
	ClientToken                   string
}

// CreateAccessControlInput is the input to CreateAccessControlConfiguration.
type CreateAccessControlInput struct {
	IndexID                       string
	Name                          string
	Description                   string
	AccessControlList             json.RawMessage
	HierarchicalAccessControlList json.RawMessage
	ClientToken                   string
}

// UpdateAccessControlInput is the input to UpdateAccessControlConfiguration.
type UpdateAccessControlInput struct {
	IndexID                       string
	ID                            string
	Name                          *string
	Description                   *string
	AccessControlList             json.RawMessage
	HierarchicalAccessControlList json.RawMessage
}

// AccessControls is the optional access control configuration capability.
//
//nolint:dupl // the capabilities share one CRUD shape by design
type AccessControls interface {
	CreateAccessControlConfiguration(ctx context.Context, in *CreateAccessControlInput) (*AccessControlConfiguration, error)
	DescribeAccessControlConfiguration(ctx context.Context, indexID, id string) (*AccessControlConfiguration, error)
	UpdateAccessControlConfiguration(ctx context.Context, in *UpdateAccessControlInput) error
	ListAccessControlConfigurations(
		ctx context.Context, indexID string, page Page,
	) (configs []AccessControlConfiguration, nextToken string, err error)
	DeleteAccessControlConfiguration(ctx context.Context, indexID, id string) error
}
