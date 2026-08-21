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
	DDLStatement      string            `json:"ddlStatement"`
	TableLimits       *tableLimitsBody  `json:"tableLimits"`
	IsAutoReclaimable *bool             `json:"isAutoReclaimable"`
	FreeformTags      map[string]string `json:"freeformTags"`
}

// changeCompartmentRequest is OCI's ChangeTableCompartmentDetails. Real OCI
// also accepts fromCompartmentId; CloudEmu moves the table wherever it
// currently sits, so naming the source would be accepted and ignored.
type changeCompartmentRequest struct {
	ToCompartmentID string `json:"toCompartmentId"`
}

// columnBody is OCI's Column model.
type columnBody struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	IsNullable   bool   `json:"isNullable"`
	DefaultValue string `json:"defaultValue,omitempty"`
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

// indexKeyBody is OCI's IndexKey model. jsonPath and jsonFieldType are absent:
// CloudEmu indexes declared columns only and refuses a JSON-path key by name.
type indexKeyBody struct {
	ColumnName string `json:"columnName"`
}

// indexBody is OCI's Index model, and the summary a listing carries.
type indexBody struct {
	Name           string         `json:"name"`
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

// updateRowRequest is OCI's UpdateRowDetails.
type updateRowRequest struct {
	CompartmentID string         `json:"compartmentId"`
	Value         map[string]any `json:"value"`
	Option        string         `json:"option"`
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

// queryRequest is OCI's QueryDetails.
type queryRequest struct {
	CompartmentID string `json:"compartmentId"`
	Statement     string `json:"statement"`
	Limit         int    `json:"limit"`
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
		out.Columns = append(out.Columns, columnBody{
			Name:         c.Name,
			Type:         c.Type,
			IsNullable:   c.IsNullable,
			DefaultValue: c.DefaultValue,
		})
	}

	return out
}

func toIndexBody(idx *nosqlprovider.Index) indexBody {
	keys := make([]indexKeyBody, 0, len(idx.Keys))
	for _, k := range idx.Keys {
		keys = append(keys, indexKeyBody{ColumnName: k.ColumnName})
	}

	return indexBody{Name: idx.Name, Keys: keys, LifecycleState: idx.LifecycleState}
}
