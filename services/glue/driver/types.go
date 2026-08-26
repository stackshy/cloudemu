package driver

import "time"

// Job-run and trigger states.
const (
	JobRunStarting  = "STARTING"
	JobRunRunning   = "RUNNING"
	JobRunSucceeded = "SUCCEEDED"
	JobRunFailed    = "FAILED"
	JobRunStopped   = "STOPPED"

	CrawlerReady    = "READY"
	CrawlerRunning  = "RUNNING"
	CrawlerStopping = "STOPPING"

	TriggerCreated     = "CREATED"
	TriggerActivated   = "ACTIVATED"
	TriggerDeactivated = "DEACTIVATED"

	WorkflowRunRunning   = "RUNNING"
	WorkflowRunCompleted = "COMPLETED"

	BlueprintRunSucceeded = "SUCCEEDED"

	SchemaVersionAvailable = "AVAILABLE"
	SchemaStatusAvailable  = "AVAILABLE"
	RegistryAvailable      = "AVAILABLE"
)

// PolicyCondition carries PutResourcePolicy's conditional-put preconditions.
// PolicyHashCondition, when set, must equal the current policy's hash.
// PolicyExistsCondition is "MUST_EXIST" or "NOT_EXIST" (empty means no check).
type PolicyCondition struct {
	PolicyHashCondition   string
	PolicyExistsCondition string
}

// Policy-existence condition values for PutResourcePolicy.
const (
	PolicyMustExist = "MUST_EXIST"
	PolicyNotExist  = "NOT_EXIST"
)

// Column is a Data Catalog column definition.
type Column struct {
	Name       string
	Type       string
	Comment    string
	Parameters map[string]string
}

// SerDeInfo describes a table's serialization/deserialization library.
type SerDeInfo struct {
	Name                 string
	SerializationLibrary string
	Parameters           map[string]string
}

// StorageDescriptor describes the physical storage of a table or partition.
type StorageDescriptor struct {
	Columns       []Column
	Location      string
	InputFormat   string
	OutputFormat  string
	Compressed    bool
	SerdeInfo     *SerDeInfo
	Parameters    map[string]string
	BucketColumns []string
	SortColumns   []Order
}

// Order is a sort-order entry in a storage descriptor.
type Order struct {
	Column    string
	SortOrder int32
}

// Database is a Data Catalog database.
type Database struct {
	CatalogID   string
	Name        string
	Description string
	LocationURI string
	Parameters  map[string]string
	CreateTime  time.Time
}

// Table is a Data Catalog table.
type Table struct {
	CatalogID         string
	DatabaseName      string
	Name              string
	Description       string
	Owner             string
	TableType         string
	StorageDescriptor *StorageDescriptor
	PartitionKeys     []Column
	Parameters        map[string]string
	ViewOriginalText  string
	ViewExpandedText  string
	CreateTime        time.Time
	UpdateTime        time.Time
	LastAccessTime    time.Time
	Retention         int32
	VersionID         string
}

// TableVersion is a stored historical version of a table.
type TableVersion struct {
	Table     Table
	VersionID string
}

// Partition is a Data Catalog partition of a table.
type Partition struct {
	CatalogID         string
	DatabaseName      string
	TableName         string
	Values            []string
	StorageDescriptor *StorageDescriptor
	Parameters        map[string]string
	CreationTime      time.Time
	LastAccessTime    time.Time
}

// UserDefinedFunction is a Data Catalog UDF.
type UserDefinedFunction struct {
	CatalogID    string
	DatabaseName string
	Name         string
	ClassName    string
	OwnerName    string
	OwnerType    string
	ResourceURIs []ResourceURI
	CreateTime   time.Time
}

// ResourceURI is a resource reference on a UDF.
type ResourceURI struct {
	ResourceType string
	URI          string
}

// Crawler is a Data Catalog crawler. SchemaChangePolicy, RecrawlPolicy and
// LineageConfiguration are round-tripped opaquely (stored as decoded JSON) so
// aws_glue_crawler does not report drift; Tags are stored in the tag store under
// the crawler ARN, matching real Glue where GetCrawler omits tags and GetTags
// returns them.
type Crawler struct {
	Name                  string
	Role                  string
	DatabaseName          string
	Description           string
	Targets               map[string]any
	Classifiers           []string
	TablePrefix           string
	State                 string
	Schedule              string
	Configuration         string
	SchemaChangePolicy    map[string]any
	RecrawlPolicy         map[string]any
	LineageConfiguration  map[string]any
	SecurityConfiguration string
	Tags                  map[string]string
	CreationTime          time.Time
	LastUpdated           time.Time
	Version               int64
	LastCrawlStatus       string
}

// Classifier is a Data Catalog classifier (grok/json/csv/xml union).
type Classifier struct {
	Name         string
	Kind         string // Grok | JSON | CSV | XML
	Definition   map[string]any
	CreationTime time.Time
	LastUpdated  time.Time
	Version      int64
}

// Connection is a Data Catalog connection.
type Connection struct {
	Name                 string
	Description          string
	ConnectionType       string
	MatchCriteria        []string
	ConnectionProperties map[string]string
	PhysicalRequirements map[string]any
	CreationTime         time.Time
	LastUpdatedTime      time.Time
}

// Job is a Glue ETL job definition. ExecutionProperty, Connections and
// NotificationProperty are round-tripped opaquely (stored as decoded JSON) so
// aws_glue_job does not report drift; Tags are stored in the tag store under the
// job ARN, matching real Glue where GetJob omits tags and GetTags returns them.
type Job struct {
	Name                  string
	Description           string
	Role                  string
	Command               map[string]any
	DefaultArguments      map[string]string
	MaxRetries            int32
	Timeout               int32
	GlueVersion           string
	WorkerType            string
	NumberOfWorkers       int32
	MaxCapacity           float64
	ExecutionProperty     map[string]any
	Connections           map[string]any
	NotificationProperty  map[string]any
	SecurityConfiguration string
	Tags                  map[string]string
	CreatedOn             time.Time
	LastModifiedOn        time.Time
}

// JobRun is a single execution of a Job.
type JobRun struct {
	ID              string
	JobName         string
	Attempt         int32
	StartedOn       time.Time
	CompletedOn     time.Time
	JobRunState     string
	Arguments       map[string]string
	ErrorMessage    string
	ExecutionTime   int32
	Timeout         int32
	WorkerType      string
	NumberOfWorkers int32
}

// Trigger is a Glue workflow trigger.
type Trigger struct {
	Name         string
	WorkflowName string
	Type         string
	State        string
	Schedule     string
	Description  string
	Actions      []map[string]any
	Predicate    map[string]any
	CreationTime time.Time
}

// Workflow is a Glue workflow.
type Workflow struct {
	Name                 string
	Description          string
	DefaultRunProperties map[string]string
	MaxConcurrentRuns    int32
	CreatedOn            time.Time
	LastModifiedOn       time.Time
}

// WorkflowRun is a single execution of a Workflow.
type WorkflowRun struct {
	Name          string
	WorkflowRunID string
	Status        string
	StartedOn     time.Time
	CompletedOn   time.Time
	RunProperties map[string]string
}

// Blueprint is a Glue blueprint.
type Blueprint struct {
	Name              string
	Description       string
	CreatedOn         time.Time
	LastModifiedOn    time.Time
	ParameterSpec     string
	BlueprintLocation string
	Status            string
}

// BlueprintRun is a single run of a blueprint.
type BlueprintRun struct {
	RunID         string
	BlueprintName string
	WorkflowName  string
	State         string
	StartedOn     time.Time
	CompletedOn   time.Time
	Parameters    string
	RoleARN       string
}

// SecurityConfiguration is a Glue security configuration.
type SecurityConfiguration struct {
	Name             string
	EncryptionConfig map[string]any
	CreatedTimeStamp time.Time
}

// Registry is a schema registry.
type Registry struct {
	Name        string
	ARN         string
	Description string
	Status      string
	CreatedTime time.Time
	UpdatedTime time.Time
}

// Schema is a schema in a registry.
type Schema struct {
	RegistryName  string
	Name          string
	ARN           string
	Description   string
	DataFormat    string
	Compatibility string
	Status        string
	CreatedTime   time.Time
	UpdatedTime   time.Time
	LatestVersion int64
	NextVersion   int64
}

// SchemaVersion is a version of a schema definition.
type SchemaVersion struct {
	SchemaName    string
	RegistryName  string
	VersionID     string
	VersionNumber int64
	Definition    string
	Status        string
	CreatedTime   time.Time
}

// DevEndpoint is a Glue development endpoint.
type DevEndpoint struct {
	EndpointName          string
	RoleARN               string
	Status                string
	WorkerType            string
	GlueVersion           string
	NumberOfWorkers       int32
	Arguments             map[string]string
	CreatedTimestamp      time.Time
	LastModifiedTimestamp time.Time
	PublicAddress         string
}

// Catalog is a Data Catalog catalog resource.
type Catalog struct {
	CatalogID   string
	Name        string
	Description string
	CreateTime  time.Time
	UpdateTime  time.Time
}

// TablePagination narrows GetTables / GetPartitions style list calls.
type TablePagination struct {
	NextToken  string
	MaxResults int32
	Expression string
}

// BatchError is a per-item failure reported by a Batch* operation, identifying
// the item (by Name and/or Values) and the error code and message.
type BatchError struct {
	Name         string
	Values       []string
	ErrorCode    string
	ErrorMessage string
}
