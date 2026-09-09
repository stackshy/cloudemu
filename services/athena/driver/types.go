package driver

import "time"

// Workgroup state values.
const (
	WorkGroupStateEnabled  = "ENABLED"
	WorkGroupStateDisabled = "DISABLED"
)

// Query-execution state values. A synchronous execution only ever reaches
// SUCCEEDED or FAILED, so those are the states modeled here.
const (
	QueryStateSucceeded = "SUCCEEDED"
	QueryStateFailed    = "FAILED"
)

// Statement-type values classified from the query text.
const (
	StatementTypeDDL     = "DDL"
	StatementTypeDML     = "DML"
	StatementTypeUtility = "UTILITY"
)

// EngineVersionAuto is the default SelectedEngineVersion.
const EngineVersionAuto = "AUTO"

// DefaultEffectiveEngineVersion is the version Athena resolves AUTO to.
const DefaultEffectiveEngineVersion = "Athena engine version 3"

// DefaultWorkGroup is the workgroup used when a caller omits one.
const DefaultWorkGroup = "primary"

// DefaultDataCatalog is the data catalog used when a caller omits one.
const DefaultDataCatalog = "AwsDataCatalog"

// Pagination carries a next token and a max-results cap for list operations.
type Pagination struct {
	NextToken  string
	MaxResults int32
}

// WorkGroup is a named collection of query settings and usage controls.
type WorkGroup struct {
	Name          string
	State         string
	Description   string
	Configuration WorkGroupConfiguration
	CreationTime  time.Time
	// Tags supplied inline on CreateWorkGroup; stored under the workgroup ARN and
	// readable via ListTagsForResource.
	Tags map[string]string
}

// WorkGroupSummary is the light projection returned by ListWorkGroups.
type WorkGroupSummary struct {
	Name          string
	State         string
	Description   string
	CreationTime  time.Time
	EngineVersion EngineVersion
}

// WorkGroupConfiguration holds the workgroup's result location, engine version,
// data-usage controls, and the override/metrics/requester-pays toggles. The
// booleans are pointers so an explicit false (user-set) is distinguishable from
// unset; the mock materializes the real defaults on create.
type WorkGroupConfiguration struct {
	ResultConfiguration             *ResultConfiguration
	EnforceWorkGroupConfiguration   *bool
	PublishCloudWatchMetricsEnabled *bool
	RequesterPaysEnabled            *bool
	BytesScannedCutoffPerQuery      *int64
	EngineVersion                   EngineVersion
}

// ResultConfiguration is where query results are written and how they are
// encrypted.
type ResultConfiguration struct {
	OutputLocation          string
	EncryptionConfiguration *EncryptionConfiguration
	ExpectedBucketOwner     string
	ACLConfiguration        *ACLConfiguration
}

// EncryptionConfiguration selects the encryption applied to query results.
type EncryptionConfiguration struct {
	EncryptionOption string
	KmsKey           string
}

// ACLConfiguration sets the canned ACL applied to query results in S3.
type ACLConfiguration struct {
	S3AclOption string
}

// EngineVersion pairs the user-selected engine version with the read-only
// effective version Athena resolves it to.
type EngineVersion struct {
	SelectedEngineVersion  string
	EffectiveEngineVersion string
}

// WorkGroupUpdate is the delta applied by UpdateWorkGroup. A nil field leaves
// the corresponding attribute unchanged.
type WorkGroupUpdate struct {
	Description          *string
	State                *string
	ConfigurationUpdates *WorkGroupConfigurationUpdates
}

// WorkGroupConfigurationUpdates carries the workgroup configuration delta.
// Remove* flags clear a value; the paired setters replace it. A nil setter with
// a false Remove flag leaves the attribute unchanged.
type WorkGroupConfigurationUpdates struct {
	EnforceWorkGroupConfiguration    *bool
	PublishCloudWatchMetricsEnabled  *bool
	RequesterPaysEnabled             *bool
	BytesScannedCutoffPerQuery       *int64
	RemoveBytesScannedCutoffPerQuery bool
	EngineVersion                    *EngineVersion
	ResultConfigurationUpdates       *ResultConfigurationUpdates
}

// ResultConfigurationUpdates is the ResultConfiguration delta. Remove* flags
// clear a value; the paired setters replace it.
type ResultConfigurationUpdates struct {
	OutputLocation                string
	RemoveOutputLocation          bool
	EncryptionConfiguration       *EncryptionConfiguration
	RemoveEncryptionConfiguration bool
	ExpectedBucketOwner           string
	RemoveExpectedBucketOwner     bool
	ACLConfiguration              *ACLConfiguration
	RemoveACLConfiguration        bool
}

// NamedQuery is a saved SQL statement.
type NamedQuery struct {
	Name         string
	Description  string
	Database     string
	QueryString  string
	NamedQueryID string
	WorkGroup    string
}

// StartQueryExecutionInput is the input to StartQueryExecution.
type StartQueryExecutionInput struct {
	QueryString           string
	ClientRequestToken    string
	QueryExecutionContext *QueryExecutionContext
	ResultConfiguration   *ResultConfiguration
	WorkGroup             string
}

// QueryExecutionContext identifies the database and catalog a query ran against.
type QueryExecutionContext struct {
	Database string
	Catalog  string
}

// QueryExecution is a single run of a query.
type QueryExecution struct {
	QueryExecutionID      string
	Query                 string
	StatementType         string
	ResultConfiguration   *ResultConfiguration
	QueryExecutionContext *QueryExecutionContext
	Status                QueryExecutionStatus
	Statistics            QueryExecutionStatistics
	WorkGroup             string
	EngineVersion         EngineVersion
	// seq is an internal monotonic ordering key (most-recent-first listing); it
	// is not part of the wire shape.
	Seq int64
}

// QueryExecutionStatus is the lifecycle status of a query execution.
type QueryExecutionStatus struct {
	State              string
	StateChangeReason  string
	SubmissionDateTime time.Time
	CompletionDateTime time.Time
}

// QueryExecutionStatistics reports the (synthetic) cost of a query execution.
type QueryExecutionStatistics struct {
	EngineExecutionTimeInMillis int64
	DataScannedInBytes          int64
	TotalExecutionTimeInMillis  int64
}

// QueryResults holds the rows a query execution produced. Emulated executions
// (DDL, and any statement with no real compute plane) return an empty result
// set with only an UpdateCount.
type QueryResults struct {
	UpdateCount int64
	ColumnInfo  []ColumnInfo
	Rows        []Row
	NextToken   string
}

// ColumnInfo describes one result column.
type ColumnInfo struct {
	Name string
	Type string
}

// Row is one result row.
type Row struct {
	Data []Datum
}

// Datum is one cell value in a result row.
type Datum struct {
	VarCharValue string
}

// Database is a Data Catalog database.
type Database struct {
	Name        string
	Description string
	Parameters  map[string]string
}

// DataCatalog is a registered data catalog.
type DataCatalog struct {
	Name        string
	Description string
	Type        string
	Parameters  map[string]string
}

// DataCatalogSummary is the light projection returned by ListDataCatalogs.
type DataCatalogSummary struct {
	CatalogName string
	Type        string
}
