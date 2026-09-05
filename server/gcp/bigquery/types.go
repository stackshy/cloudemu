package bigquery

import (
	"strconv"
	"strings"
	"time"
)

// int64Wire is a BigQuery int64 wire field. BigQuery emits int64 values as
// quoted decimal STRINGS, but clients (the Terraform google provider included)
// send them as bare JSON NUMBERS on write — so it marshals to a string and
// unmarshals from either a number or a quoted string. A zero value with
// omitempty is dropped from the response, matching an unset field.
type int64Wire int64

// MarshalJSON emits the value as a quoted decimal string.
func (v int64Wire) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatInt(int64(v), 10))), nil
}

// UnmarshalJSON accepts a JSON number or a quoted decimal string.
func (v *int64Wire) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*v = 0
		return nil
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}

	*v = int64Wire(n)

	return nil
}

// Wire kind discriminators BigQuery stamps on each resource / list envelope.
const (
	kindDataset     = "bigquery#dataset"
	kindTable       = "bigquery#table"
	kindDatasetList = "bigquery#datasetList"
	kindTableList   = "bigquery#tableList"

	// modeNullable is the default TableFieldSchema mode. BigQuery echoes it on
	// every field that omits a mode, so a client that sent no mode reads back
	// NULLABLE — omitting it drifts Terraform.
	modeNullable = "NULLABLE"
)

// wireDatasetRef mirrors datasetReference.
type wireDatasetRef struct {
	ProjectID string `json:"projectId,omitempty"`
	DatasetID string `json:"datasetId,omitempty"`
}

// wireTableRef mirrors tableReference.
type wireTableRef struct {
	ProjectID string `json:"projectId,omitempty"`
	DatasetID string `json:"datasetId,omitempty"`
	TableID   string `json:"tableId,omitempty"`
}

// wireRoutineRef mirrors routineReference.
type wireRoutineRef struct {
	ProjectID string `json:"projectId,omitempty"`
	DatasetID string `json:"datasetId,omitempty"`
	RoutineID string `json:"routineId,omitempty"`
}

// wireDatasetAccess mirrors an access[].dataset grant.
type wireDatasetAccess struct {
	Dataset     *wireDatasetRef `json:"dataset,omitempty"`
	TargetTypes []string        `json:"targetTypes,omitempty"`
}

// wireAccess mirrors one access[] entry.
type wireAccess struct {
	Role         string             `json:"role,omitempty"`
	UserByEmail  string             `json:"userByEmail,omitempty"`
	GroupByEmail string             `json:"groupByEmail,omitempty"`
	SpecialGroup string             `json:"specialGroup,omitempty"`
	Domain       string             `json:"domain,omitempty"`
	IamMember    string             `json:"iamMember,omitempty"`
	View         *wireTableRef      `json:"view,omitempty"`
	Routine      *wireRoutineRef    `json:"routine,omitempty"`
	Dataset      *wireDatasetAccess `json:"dataset,omitempty"`
}

// wireDataset is the datasets resource wire shape.
type wireDataset struct {
	Kind                     string            `json:"kind,omitempty"`
	Etag                     string            `json:"etag,omitempty"`
	ID                       string            `json:"id,omitempty"`
	SelfLink                 string            `json:"selfLink,omitempty"`
	DatasetReference         *wireDatasetRef   `json:"datasetReference,omitempty"`
	FriendlyName             string            `json:"friendlyName,omitempty"`
	Description              string            `json:"description,omitempty"`
	DefaultTableExpirationMs int64Wire         `json:"defaultTableExpirationMs,omitempty"`
	Labels                   map[string]string `json:"labels,omitempty"`
	Access                   []wireAccess      `json:"access,omitempty"`
	Location                 string            `json:"location,omitempty"`
	CreationTime             string            `json:"creationTime,omitempty"`
	LastModifiedTime         string            `json:"lastModifiedTime,omitempty"`
}

// wireField is one schema column, recursive for RECORD/STRUCT nesting.
type wireField struct {
	Name        string      `json:"name"`
	Type        string      `json:"type,omitempty"`
	Mode        string      `json:"mode,omitempty"`
	Description string      `json:"description,omitempty"`
	Fields      []wireField `json:"fields,omitempty"`
}

// wireSchema wraps the field list.
type wireSchema struct {
	Fields []wireField `json:"fields,omitempty"`
}

// wireTimePartitioning mirrors timePartitioning.
type wireTimePartitioning struct {
	Type         string    `json:"type,omitempty"`
	Field        string    `json:"field,omitempty"`
	ExpirationMs int64Wire `json:"expirationMs,omitempty"`
}

// wireClustering mirrors clustering.
type wireClustering struct {
	Fields []string `json:"fields,omitempty"`
}

// wireView mirrors a view definition.
type wireView struct {
	Query        string `json:"query,omitempty"`
	UseLegacySQL *bool  `json:"useLegacySql,omitempty"`
}

// wireTable is the tables resource wire shape.
type wireTable struct {
	Kind             string                `json:"kind,omitempty"`
	Etag             string                `json:"etag,omitempty"`
	ID               string                `json:"id,omitempty"`
	SelfLink         string                `json:"selfLink,omitempty"`
	TableReference   *wireTableRef         `json:"tableReference,omitempty"`
	FriendlyName     string                `json:"friendlyName,omitempty"`
	Description      string                `json:"description,omitempty"`
	Schema           *wireSchema           `json:"schema,omitempty"`
	Type             string                `json:"type,omitempty"`
	Labels           map[string]string     `json:"labels,omitempty"`
	TimePartitioning *wireTimePartitioning `json:"timePartitioning,omitempty"`
	Clustering       *wireClustering       `json:"clustering,omitempty"`
	View             *wireView             `json:"view,omitempty"`
	NumRows          string                `json:"numRows,omitempty"`
	NumBytes         string                `json:"numBytes,omitempty"`
	CreationTime     string                `json:"creationTime,omitempty"`
	LastModifiedTime string                `json:"lastModifiedTime,omitempty"`
	ExpirationTime   int64Wire             `json:"expirationTime,omitempty"`
}

// epochMillis formats a time as epoch-milliseconds decimal string (BigQuery's
// timestamp wire form), or "" for the zero time.
func epochMillis(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return strconv.FormatInt(t.UnixMilli(), 10)
}
