// Package driver defines the BigQuery metadata control-plane interface and
// its resource types. It covers the dataset and table surface real
// google.golang.org/api/bigquery/v2 clients, gcloud, and the Terraform
// google_bigquery_dataset / google_bigquery_table resources drive.
//
// Types here are semantic (time.Time, int64, recursive Field trees); the GCP
// wire handler (server/gcp/bigquery) owns the wire representation — the
// epoch-millis-string timestamps, the quoted-int64 counters, and the
// "{project}:{dataset}[.{table}]" id format BigQuery emits.
//
// Scope is the metadata control plane only: dataset + table CRUD, list, and
// full schema round-trip. Query job execution, streaming inserts, and the
// other data-plane surfaces are out of scope (see the buildout backlog).
package driver

import (
	"context"
	"time"
)

// BigQuery is the metadata control plane for datasets and tables.
type BigQuery interface {
	// InsertDataset creates a dataset under project. It returns AlreadyExists
	// when the datasetId is already taken.
	InsertDataset(ctx context.Context, project string, ds *Dataset) (*Dataset, error)
	// GetDataset returns the dataset, or NotFound.
	GetDataset(ctx context.Context, project, datasetID string) (*Dataset, error)
	// ListDatasets returns every dataset in project, ordered by datasetId.
	ListDatasets(ctx context.Context, project string) ([]*Dataset, error)
	// PatchDataset merges the supplied fields of patch into the dataset (the
	// datasets.patch / HTTP PATCH semantics: labels are merged, other supplied
	// fields overwrite). It returns NotFound when the dataset is absent.
	PatchDataset(ctx context.Context, project, datasetID string, patch *DatasetPatch) (*Dataset, error)
	// UpdateDataset replaces the dataset's mutable fields with the supplied
	// patch (the datasets.update / HTTP PUT semantics: an omitted field is
	// cleared).
	UpdateDataset(ctx context.Context, project, datasetID string, ds *DatasetPatch) (*Dataset, error)
	// DeleteDataset removes the dataset. When deleteContents is false and the
	// dataset still holds tables, it returns FailedPrecondition.
	DeleteDataset(ctx context.Context, project, datasetID string, deleteContents bool) error

	// InsertTable creates a table under project/datasetID. It returns
	// AlreadyExists when the tableId is taken, NotFound when the dataset is
	// absent.
	InsertTable(ctx context.Context, project, datasetID string, tbl *Table) (*Table, error)
	// GetTable returns the table, or NotFound.
	GetTable(ctx context.Context, project, datasetID, tableID string) (*Table, error)
	// ListTables returns every table in the dataset, ordered by tableId.
	ListTables(ctx context.Context, project, datasetID string) ([]*Table, error)
	// PatchTable merges the supplied fields of patch into the table (labels are
	// merged; a supplied schema/other field overwrites). NotFound when absent.
	PatchTable(ctx context.Context, project, datasetID, tableID string, patch *TablePatch) (*Table, error)
	// UpdateTable replaces the table's mutable fields with the supplied patch.
	UpdateTable(ctx context.Context, project, datasetID, tableID string, tbl *TablePatch) (*Table, error)
	// DeleteTable removes the table, or NotFound.
	DeleteTable(ctx context.Context, project, datasetID, tableID string) error
}

// Dataset is BigQuery dataset metadata.
type Dataset struct {
	ProjectID string
	DatasetID string

	FriendlyName string
	Description  string
	// DefaultTableExpirationMs is the default table lifetime in milliseconds;
	// 0 means unset (BigQuery has no valid 0 expiration).
	DefaultTableExpirationMs int64
	Location                 string
	Labels                   map[string]string
	Access                   []AccessEntry

	Etag             string
	CreationTime     time.Time
	LastModifiedTime time.Time
}

// DatasetPatch carries the mutable dataset fields a patch/update request
// supplies. A nil pointer field was omitted by the caller; patch leaves an
// omitted field unchanged, update clears it.
type DatasetPatch struct {
	FriendlyName             *string
	Description              *string
	DefaultTableExpirationMs *int64
	Location                 *string
	Labels                   map[string]string
	LabelsSet                bool // true when the request supplied a labels object
	Access                   []AccessEntry
	AccessSet                bool // true when the request supplied an access array
	// Etag, when non-empty, is an optimistic-concurrency precondition: the
	// request is rejected with FailedPrecondition when it does not match the
	// dataset's current etag.
	Etag string
}

// AccessEntry is one dataset access-control grant. Only one of the target
// fields is set per entry; unset fields round-trip as absent.
type AccessEntry struct {
	Role         string
	UserByEmail  string
	GroupByEmail string
	SpecialGroup string
	Domain       string
	IamMember    string
	View         *TableReference
	Routine      *RoutineReference
	Dataset      *DatasetAccessEntry
}

// DatasetAccessEntry grants access to all tables of another dataset.
type DatasetAccessEntry struct {
	Dataset     *DatasetReference
	TargetTypes []string
}

// DatasetReference identifies a dataset.
type DatasetReference struct {
	ProjectID string
	DatasetID string
}

// RoutineReference identifies a routine granted dataset access.
type RoutineReference struct {
	ProjectID string
	DatasetID string
	RoutineID string
}

// Table is BigQuery table (or view) metadata.
type Table struct {
	ProjectID string
	DatasetID string
	TableID   string

	FriendlyName string
	Description  string
	Type         string // TABLE or VIEW; defaults to TABLE
	Schema       []Field
	Labels       map[string]string

	TimePartitioning *TimePartitioning
	Clustering       []string
	View             *ViewDefinition

	NumRows  int64
	NumBytes int64

	Etag             string
	CreationTime     time.Time
	LastModifiedTime time.Time
	// ExpirationTime is the table's absolute expiry; zero means no expiry.
	ExpirationTime time.Time
}

// TablePatch carries the mutable table fields a patch/update supplies.
type TablePatch struct {
	FriendlyName     *string
	Description      *string
	Schema           []Field
	SchemaSet        bool // true when the request supplied a schema (even empty)
	Labels           map[string]string
	LabelsSet        bool
	TimePartitioning *TimePartitioning
	Clustering       []string
	ClusteringSet    bool
	View             *ViewDefinition
	ExpirationTime   *time.Time
	Etag             string
}

// TableReference identifies a table.
type TableReference struct {
	ProjectID string
	DatasetID string
	TableID   string
}

// Field is one schema column, recursive for RECORD/STRUCT nesting.
type Field struct {
	Name        string
	Type        string
	Mode        string // NULLABLE (default), REQUIRED, or REPEATED
	Description string
	Fields      []Field // nested fields for a RECORD/STRUCT column
}

// TimePartitioning is a table's time-based partitioning spec.
type TimePartitioning struct {
	Type         string
	Field        string
	ExpirationMs int64
}

// ViewDefinition is a logical view's SQL. It round-trips as metadata; the
// query is never executed by the control plane.
type ViewDefinition struct {
	Query        string
	UseLegacySQL bool
}
