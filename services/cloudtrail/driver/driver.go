// Package driver defines the interface and types for AWS CloudTrail
// implementations. It models trails (with logging status), event data stores,
// channels, dashboards, imports, event/insight selectors, ad-hoc queries, tags,
// and the read-only lookup/insight surfaces. Trails are referenced by name or
// ARN; event data stores, channels, dashboards, and imports by ARN/ID.
package driver

import (
	"context"
	"time"
)

// Event data store statuses.
const (
	EDSStatusCreated              = "CREATED"
	EDSStatusEnabled              = "ENABLED"
	EDSStatusPendingDeletion      = "PENDING_DELETION"
	EDSStatusStartingIngestion    = "STARTING_INGESTION"
	EDSStatusStoppedIngestion     = "STOPPED_INGESTION"
	EDSStatusStoppingIngestion    = "STOPPING_INGESTION"
	EDSStatusStartingIngestionAlt = "STARTED_INGESTION"
)

// Billing modes for event data stores.
const (
	BillingExtendableRetention = "EXTENDABLE_RETENTION_PRICING"
	BillingFixedRetention      = "FIXED_RETENTION_PRICING"
)

// Query statuses.
const (
	QueryStatusQueued    = "QUEUED"
	QueryStatusRunning   = "RUNNING"
	QueryStatusFinished  = "FINISHED"
	QueryStatusFailed    = "FAILED"
	QueryStatusCancelled = "CANCELLED" //nolint:misspell // AWS CloudTrail spells the query status "CANCELLED"
	QueryStatusTimedOut  = "TIMED_OUT"
)

// Import statuses.
const (
	ImportStatusInitializing = "INITIALIZING"
	ImportStatusInProgress   = "IN_PROGRESS"
	ImportStatusFailed       = "FAILED"
	ImportStatusStopped      = "STOPPED"
	ImportStatusCompleted    = "COMPLETED"
)

// Trail holds a trail's stored configuration (the CloudTrail Trail shape).
type Trail struct {
	Name                       string
	TrailARN                   string
	S3BucketName               string
	S3KeyPrefix                string
	SNSTopicName               string
	SNSTopicARN                string
	IncludeGlobalServiceEvents bool
	IsMultiRegionTrail         bool
	IsOrganizationTrail        bool
	HomeRegion                 string
	LogFileValidationEnabled   bool
	CloudWatchLogsLogGroupARN  string
	CloudWatchLogsRoleARN      string
	KMSKeyID                   string
	HasCustomEventSelectors    bool
	HasInsightSelectors        bool
	CreatedAt                  time.Time
}

// TrailStatus is the logging status of a trail (GetTrailStatus shape).
type TrailStatus struct {
	IsLogging          bool
	StartLoggingTime   time.Time
	StopLoggingTime    time.Time
	LatestDeliveryTime time.Time
}

// CreateTrailInput describes a trail to create.
type CreateTrailInput struct {
	Name                       string
	S3BucketName               string
	S3KeyPrefix                string
	SNSTopicName               string
	IncludeGlobalServiceEvents *bool
	IsMultiRegionTrail         bool
	IsOrganizationTrail        bool
	LogFileValidationEnabled   bool
	CloudWatchLogsLogGroupARN  string
	CloudWatchLogsRoleARN      string
	KMSKeyID                   string
	Tags                       map[string]string
}

// UpdateTrailInput describes trail settings to update. Pointer fields left nil
// keep the current value.
type UpdateTrailInput struct {
	Name                       string
	S3BucketName               *string
	S3KeyPrefix                *string
	SNSTopicName               *string
	IncludeGlobalServiceEvents *bool
	IsMultiRegionTrail         *bool
	IsOrganizationTrail        *bool
	LogFileValidationEnabled   *bool
	CloudWatchLogsLogGroupARN  *string
	CloudWatchLogsRoleARN      *string
	KMSKeyID                   *string
}

// DataResource is one data-resource entry of an event selector.
type DataResource struct {
	Type   string
	Values []string
}

// EventSelector is a basic event selector for a trail.
type EventSelector struct {
	ReadWriteType                 string
	IncludeManagementEvents       *bool
	DataResources                 []DataResource
	ExcludeManagementEventSources []string
}

// AdvancedFieldSelector is one field selector of an advanced event selector.
type AdvancedFieldSelector struct {
	Field         string
	Equals        []string
	NotEquals     []string
	StartsWith    []string
	NotStartsWith []string
	EndsWith      []string
	NotEndsWith   []string
}

// AdvancedEventSelector is a fine-grained event selector for a trail or EDS.
type AdvancedEventSelector struct {
	Name           string
	FieldSelectors []AdvancedFieldSelector
}

// InsightSelector selects an insight type (ApiCallRateInsight / ApiErrorRateInsight).
type InsightSelector struct {
	InsightType string
}

// EventDataStore holds an event data store's stored configuration.
type EventDataStore struct {
	Name                         string
	ARN                          string
	Status                       string
	BillingMode                  string
	RetentionPeriod              int32
	MultiRegionEnabled           bool
	OrganizationEnabled          bool
	TerminationProtectionEnabled bool
	KMSKeyID                     string
	AdvancedEventSelectors       []AdvancedEventSelector
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Tags                         map[string]string
}

// CreateEventDataStoreInput describes an event data store to create.
type CreateEventDataStoreInput struct {
	Name                         string
	BillingMode                  string
	RetentionPeriod              *int32
	MultiRegionEnabled           *bool
	OrganizationEnabled          *bool
	TerminationProtectionEnabled *bool
	StartIngestion               *bool
	KMSKeyID                     string
	AdvancedEventSelectors       []AdvancedEventSelector
	Tags                         map[string]string
}

// UpdateEventDataStoreInput describes event data store settings to update.
type UpdateEventDataStoreInput struct {
	ARN                          string
	Name                         *string
	BillingMode                  *string
	RetentionPeriod              *int32
	MultiRegionEnabled           *bool
	OrganizationEnabled          *bool
	TerminationProtectionEnabled *bool
	KMSKeyID                     *string
	AdvancedEventSelectors       []AdvancedEventSelector
}

// Destination is a channel destination.
type Destination struct {
	Type     string
	Location string
}

// Channel holds a channel's stored configuration.
type Channel struct {
	Name                   string
	ARN                    string
	Source                 string
	Destinations           []Destination
	AdvancedEventSelectors []AdvancedEventSelector
	CreatedAt              time.Time
	Tags                   map[string]string
}

// Dashboard holds a dashboard's stored configuration.
type Dashboard struct {
	Name                         string
	ARN                          string
	Type                         string
	TerminationProtectionEnabled bool
	RefreshScheduleFrequencyUnit string
	RefreshScheduleFrequencyVal  int32
	RefreshScheduleStatus        string
	Status                       string
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Tags                         map[string]string
}

// Import holds an import job's stored state.
type Import struct {
	ID                    string
	Status                string
	Destinations          []string
	S3LocationURI         string
	S3BucketRegion        string
	S3BucketAccessRoleARN string
	StartEventTime        time.Time
	EndEventTime          time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Query holds an ad-hoc query's stored state.
type Query struct {
	ID               string
	QueryString      string
	EventDataStoreID string
	Status           string
	CreatedAt        time.Time
	Prompt           string
	DeliveryS3URI    string
}

// LookupAttribute narrows a LookupEvents call.
type LookupAttribute struct {
	AttributeKey   string
	AttributeValue string
}

// LookupInput narrows LookupEvents.
type LookupInput struct {
	LookupAttributes []LookupAttribute
	StartTime        time.Time
	EndTime          time.Time
	EventCategory    string
	NextToken        string
	MaxResults       int32
}

// Event is one management event returned by LookupEvents.
type Event struct {
	EventID         string
	EventName       string
	EventTime       time.Time
	EventSource     string
	Username        string
	CloudTrailEvent string
	AccessKeyID     string
	ReadOnly        string
}

// PublicKey is a digest-signing public key returned by ListPublicKeys.
type PublicKey struct {
	Fingerprint       string
	Value             []byte
	ValidityStartTime time.Time
	ValidityEndTime   time.Time
}

// CloudTrail is the interface a CloudTrail backend implements.
type CloudTrail interface {
	// Trails.
	CreateTrail(ctx context.Context, in CreateTrailInput) (*Trail, error)
	GetTrail(ctx context.Context, nameOrARN string) (*Trail, error)
	UpdateTrail(ctx context.Context, in UpdateTrailInput) (*Trail, error)
	DeleteTrail(ctx context.Context, nameOrARN string) error
	DescribeTrails(ctx context.Context, nameList []string) ([]Trail, error)
	ListTrails(ctx context.Context, nextToken string) ([]Trail, string, error)
	GetTrailStatus(ctx context.Context, nameOrARN string) (*TrailStatus, error)
	StartLogging(ctx context.Context, nameOrARN string) error
	StopLogging(ctx context.Context, nameOrARN string) error

	// Event/insight selectors.
	PutEventSelectors(ctx context.Context, trailName string, sel []EventSelector, adv []AdvancedEventSelector) (
		trailARN string, outSel []EventSelector, outAdv []AdvancedEventSelector, err error)
	GetEventSelectors(ctx context.Context, trailName string) (
		trailARN string, sel []EventSelector, adv []AdvancedEventSelector, err error)
	PutInsightSelectors(ctx context.Context, trailName, edsARN string, sel []InsightSelector) (
		trailARN, outEDSARN string, out []InsightSelector, err error)
	GetInsightSelectors(ctx context.Context, trailName, edsARN string) (
		trailARN, outEDSARN string, out []InsightSelector, err error)

	// Event data stores.
	CreateEventDataStore(ctx context.Context, in CreateEventDataStoreInput) (*EventDataStore, error)
	GetEventDataStore(ctx context.Context, arn string) (*EventDataStore, error)
	UpdateEventDataStore(ctx context.Context, in UpdateEventDataStoreInput) (*EventDataStore, error)
	DeleteEventDataStore(ctx context.Context, arn string) error
	RestoreEventDataStore(ctx context.Context, arn string) (*EventDataStore, error)
	ListEventDataStores(ctx context.Context, nextToken string, maxResults int32) ([]EventDataStore, string, error)
	StartEventDataStoreIngestion(ctx context.Context, arn string) error
	StopEventDataStoreIngestion(ctx context.Context, arn string) error

	// Channels.
	CreateChannel(ctx context.Context, name, source string, dests []Destination, tags map[string]string) (*Channel, error)
	GetChannel(ctx context.Context, arn string) (*Channel, error)
	UpdateChannel(ctx context.Context, arn, name string, dests []Destination) (*Channel, error)
	DeleteChannel(ctx context.Context, arn string) error
	ListChannels(ctx context.Context, nextToken string, maxResults int32) ([]Channel, string, error)

	// Dashboards.
	CreateDashboard(ctx context.Context, in Dashboard) (*Dashboard, error)
	GetDashboard(ctx context.Context, nameOrARN string) (*Dashboard, error)
	UpdateDashboard(ctx context.Context, in Dashboard) (*Dashboard, error)
	DeleteDashboard(ctx context.Context, nameOrARN string) error
	ListDashboards(ctx context.Context, nextToken string, maxResults int32) ([]Dashboard, string, error)
	StartDashboardRefresh(ctx context.Context, nameOrARN string) (string, error)

	// Imports.
	StartImport(ctx context.Context, in Import) (*Import, error)
	GetImport(ctx context.Context, id string) (*Import, error)
	StopImport(ctx context.Context, id string) (*Import, error)
	ListImports(ctx context.Context, dest, importStatus, nextToken string, maxResults int32) ([]Import, string, error)
	ListImportFailures(ctx context.Context, id, nextToken string, maxResults int32) ([]ImportFailure, string, error)

	// Queries (ad-hoc analysis over event data stores).
	StartQuery(ctx context.Context, edsID, queryString, deliveryS3URI, queryStatement string) (string, error)
	DescribeQuery(ctx context.Context, edsID, queryID, alias string) (*Query, error)
	GetQueryResults(ctx context.Context, edsID, queryID, nextToken string, maxResults int32) (*QueryResults, error)
	CancelQuery(ctx context.Context, edsID, queryID string) (queryStatus string, err error)
	ListQueries(ctx context.Context, edsID, nextToken string, maxResults int32) ([]Query, string, error)
	GenerateQuery(ctx context.Context, edsIDs []string, prompt string) (queryAlias, queryStatement string, err error)

	// Resource policy.
	PutResourcePolicy(ctx context.Context, resourceARN, policy string) (outARN, outPolicy string, err error)
	GetResourcePolicy(ctx context.Context, resourceARN string) (outARN, policy string, err error)
	DeleteResourcePolicy(ctx context.Context, resourceARN string) error

	// Event configuration (per EDS/channel).
	PutEventConfiguration(ctx context.Context, resourceARN string, maxEventSize string) (
		outARN, outMaxEventSize string, err error)
	GetEventConfiguration(ctx context.Context, resourceARN string) (outARN, maxEventSize string, err error)

	// Federation (EDS Lake Formation federation).
	EnableFederation(ctx context.Context, edsARN, roleARN string) (outARN, federationRoleARN, federationStatus string, err error)
	DisableFederation(ctx context.Context, edsARN string) (outARN, federationStatus string, err error)

	// Tags.
	AddTags(ctx context.Context, resourceID string, tags map[string]string) error
	RemoveTags(ctx context.Context, resourceID string, tagKeys []string) error
	ListTags(ctx context.Context, resourceIDs []string) (map[string]map[string]string, error)

	// Organization delegated admin.
	RegisterOrganizationDelegatedAdmin(ctx context.Context, memberAccountID string) error
	DeregisterOrganizationDelegatedAdmin(ctx context.Context, delegatedAdminAccountID string) error

	// Read-only lookup/insight/keys/samples (synthesized; documented).
	LookupEvents(ctx context.Context, in LookupInput) ([]Event, string, error)
	ListPublicKeys(ctx context.Context, start, end time.Time, nextToken string) ([]PublicKey, string, error)
	ListInsightsData(ctx context.Context, nextToken string) ([]InsightDataPoint, string, error)
	ListInsightsMetricData(ctx context.Context, nextToken string) ([]InsightMetricPoint, string, error)
	SearchSampleQueries(ctx context.Context, searchPhrase, nextToken string, maxResults int32) (
		[]SampleQuery, string, error)
}

// ImportFailure is one failed import item.
type ImportFailure struct {
	Location     string
	Status       string
	ErrorType    string
	ErrorMessage string
	LastUpdated  time.Time
}

// QueryResults is the result page of GetQueryResults.
type QueryResults struct {
	QueryStatus     string
	ResultRows      []map[string]string
	NextToken       string
	ErrorMessage    string
	TotalResultsCnt int64
}

// InsightDataPoint is one row of ListInsightsData (synthesized/empty).
type InsightDataPoint struct {
	EventName   string
	InsightType string
	Timestamp   time.Time
}

// InsightMetricPoint is one row of ListInsightsMetricData (synthesized/empty).
type InsightMetricPoint struct {
	Timestamp time.Time
	Value     float64
}

// SampleQuery is one CloudTrail Lake sample query.
type SampleQuery struct {
	Name        string
	Description string
	SQL         string
	Relevance   float32
}
