package nosql

import (
	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
)

// tableLimitsBody is OCI's TableLimits model.
type tableLimitsBody struct {
	MaxReadUnits    int    `json:"maxReadUnits"`
	MaxWriteUnits   int    `json:"maxWriteUnits"`
	MaxStorageInGBs int    `json:"maxStorageInGBs"`
	CapacityMode    string `json:"capacityMode,omitempty"`
}

// createTableRequest is OCI's CreateTableDetails.
type createTableRequest struct {
	Name              string            `json:"name"`
	CompartmentID     string            `json:"compartmentId"`
	DDLStatement      string            `json:"ddlStatement"`
	TableLimits       *tableLimitsBody  `json:"tableLimits"`
	IsAutoReclaimable bool              `json:"isAutoReclaimable"`
	FreeformTags      map[string]string `json:"freeformTags"`
}

// updateTableRequest is OCI's UpdateTableDetails. Every field is optional, so
// the pointers distinguish "absent" from "set to the zero value".
type updateTableRequest struct {
	CompartmentID     string            `json:"compartmentId"`
	DDLStatement      string            `json:"ddlStatement"`
	TableLimits       *tableLimitsBody  `json:"tableLimits"`
	IsAutoReclaimable *bool             `json:"isAutoReclaimable"`
	FreeformTags      map[string]string `json:"freeformTags"`
}

// changeCompartmentRequest is OCI's ChangeTableCompartmentDetails.
// fromCompartmentId scopes the table name in the path, which is unique within
// a compartment only; it is redundant when the path carries an OCID.
type changeCompartmentRequest struct {
	FromCompartmentID string `json:"fromCompartmentId"`
	ToCompartmentID   string `json:"toCompartmentId"`
}

// columnBody is OCI's Column model.
type columnBody struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	IsNullable   bool    `json:"isNullable"`
	DefaultValue *string `json:"defaultValue,omitempty"`
}

// schemaBody is OCI's Schema model. ttl is in days, which is the only unit
// the model can carry.
type schemaBody struct {
	Columns    []columnBody `json:"columns"`
	PrimaryKey []string     `json:"primaryKey"`
	ShardKey   []string     `json:"shardKey"`
	TTL        int          `json:"ttl"`
}

// tableBody is OCI's Table model, and the summary a listing carries.
type tableBody struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	CompartmentID     string            `json:"compartmentId"`
	TimeCreated       string            `json:"timeCreated"`
	TimeUpdated       string            `json:"timeUpdated"`
	TableLimits       tableLimitsBody   `json:"tableLimits"`
	LifecycleState    string            `json:"lifecycleState"`
	IsAutoReclaimable bool              `json:"isAutoReclaimable"`
	DDLStatement      string            `json:"ddlStatement"`
	Schema            schemaBody        `json:"schema"`
	FreeformTags      map[string]string `json:"freeformTags,omitempty"`
}

// tableCollection is OCI's TableCollection.
type tableCollection struct {
	Items []tableBody `json:"items"`
}

// indexKeyBody is OCI's IndexKey model. CloudEmu indexes declared columns
// only, so createIndex refuses a key that sets jsonPath or jsonFieldType.
type indexKeyBody struct {
	ColumnName    string `json:"columnName"`
	JSONPath      string `json:"jsonPath,omitempty"`
	JSONFieldType string `json:"jsonFieldType,omitempty"`
}

// indexBody is OCI's Index model, and the summary a listing carries.
type indexBody struct {
	Name           string         `json:"name"`
	CompartmentID  string         `json:"compartmentId"`
	TableName      string         `json:"tableName"`
	TableID        string         `json:"tableId"`
	Keys           []indexKeyBody `json:"keys"`
	LifecycleState string         `json:"lifecycleState"`
}

// indexCollection is OCI's IndexCollection.
type indexCollection struct {
	Items []indexBody `json:"items"`
}

// createIndexRequest is OCI's CreateIndexDetails.
type createIndexRequest struct {
	Name          string         `json:"name"`
	CompartmentID string         `json:"compartmentId"`
	Keys          []indexKeyBody `json:"keys"`
	IsIfNotExists bool           `json:"isIfNotExists"`
}

// updateRowRequest is OCI's UpdateRowDetails. ttl is in days.
type updateRowRequest struct {
	CompartmentID        string         `json:"compartmentId"`
	Value                map[string]any `json:"value"`
	Option               string         `json:"option"`
	IsGetReturnRow       bool           `json:"isGetReturnRow"`
	TTL                  *int           `json:"ttl"`
	IsTTLUseTableDefault bool           `json:"isTtlUseTableDefault"`
	IsExactMatch         bool           `json:"isExactMatch"`
}

// updateRowResult is OCI's UpdateRowResult. version is null when the write's
// option refused it; the row in the way rides along when isGetReturnRow asks.
type updateRowResult struct {
	Version         *string        `json:"version"`
	ExistingVersion string         `json:"existingVersion,omitempty"`
	ExistingValue   map[string]any `json:"existingValue,omitempty"`
}

// rowBody is OCI's Row model. usage is absent: CloudEmu does not meter read
// and write unit consumption, so reporting zeros would read as real telemetry.
type rowBody struct {
	Value            map[string]any `json:"value"`
	TimeOfExpiration string         `json:"timeOfExpiration,omitempty"`
}

// deleteRowResult is OCI's DeleteRowResult.
type deleteRowResult struct {
	IsSuccess bool `json:"isSuccess"`
}

// queryRequest is OCI's QueryDetails. limit and page are query parameters,
// which paginate reads.
type queryRequest struct {
	CompartmentID string `json:"compartmentId"`
	Statement     string `json:"statement"`
	IsPrepared    bool   `json:"isPrepared"`
}

// queryResultCollection is OCI's QueryResultCollection.
type queryResultCollection struct {
	Items []map[string]any `json:"items"`
}

func toTableBody(t *nosqlprovider.Table) tableBody {
	return tableBody{
		ID:                t.ID,
		Name:              t.Name,
		CompartmentID:     t.CompartmentID,
		TimeCreated:       t.TimeCreated,
		TimeUpdated:       t.TimeUpdated,
		TableLimits:       toLimitsBody(t.Limits),
		LifecycleState:    t.LifecycleState,
		IsAutoReclaimable: t.IsAutoReclaimable,
		DDLStatement:      t.DDLStatement,
		Schema:            toSchemaBody(&t.Schema),
		FreeformTags:      t.FreeformTags,
	}
}

func toLimitsBody(l nosqlprovider.TableLimits) tableLimitsBody {
	return tableLimitsBody{
		MaxReadUnits:    l.MaxReadUnits,
		MaxWriteUnits:   l.MaxWriteUnits,
		MaxStorageInGBs: l.MaxStorageInGBs,
		CapacityMode:    l.CapacityMode,
	}
}

func fromLimitsBody(b *tableLimitsBody) nosqlprovider.TableLimits {
	return nosqlprovider.TableLimits{
		MaxReadUnits:    b.MaxReadUnits,
		MaxWriteUnits:   b.MaxWriteUnits,
		MaxStorageInGBs: b.MaxStorageInGBs,
		CapacityMode:    b.CapacityMode,
	}
}

func toSchemaBody(s *nosqlprovider.Schema) schemaBody {
	out := schemaBody{
		Columns:    make([]columnBody, 0, len(s.Columns)),
		PrimaryKey: s.PrimaryKey,
		ShardKey:   s.ShardKey,
		TTL:        s.TTL.Days,
	}

	for _, c := range s.Columns {
		col := columnBody{Name: c.Name, Type: c.Type, IsNullable: c.IsNullable}
		if c.HasDefault {
			col.DefaultValue = &c.DefaultValue
		}

		out.Columns = append(out.Columns, col)
	}

	return out
}

func toIndexBody(idx *nosqlprovider.Index, table *nosqlprovider.Table) indexBody {
	keys := make([]indexKeyBody, 0, len(idx.Keys))
	for _, k := range idx.Keys {
		keys = append(keys, indexKeyBody{ColumnName: k.ColumnName})
	}

	return indexBody{
		Name:           idx.Name,
		CompartmentID:  table.CompartmentID,
		TableName:      table.Name,
		TableID:        table.ID,
		Keys:           keys,
		LifecycleState: idx.LifecycleState,
	}
}
