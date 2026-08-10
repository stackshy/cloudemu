package glue

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// epochOrNil renders a time as a Unix-epoch float the Glue SDK decodes into a
// *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// --- shared nested wire shapes ---

type columnJSON struct {
	Name       string            `json:"Name"`
	Type       string            `json:"Type,omitempty"`
	Comment    string            `json:"Comment,omitempty"`
	Parameters map[string]string `json:"Parameters,omitempty"`
}

func columnsToWire(in []driver.Column) []columnJSON {
	if in == nil {
		return nil
	}

	out := make([]columnJSON, len(in))
	for i := range in {
		out[i] = columnJSON{Name: in[i].Name, Type: in[i].Type, Comment: in[i].Comment, Parameters: in[i].Parameters}
	}

	return out
}

func columnsFromWire(in []columnJSON) []driver.Column {
	if in == nil {
		return nil
	}

	out := make([]driver.Column, len(in))
	for i := range in {
		out[i] = driver.Column{Name: in[i].Name, Type: in[i].Type, Comment: in[i].Comment, Parameters: in[i].Parameters}
	}

	return out
}

type orderJSON struct {
	Column    string `json:"Column"`
	SortOrder int32  `json:"SortOrder"`
}

type serDeInfoJSON struct {
	Name                 string            `json:"Name,omitempty"`
	SerializationLibrary string            `json:"SerializationLibrary,omitempty"`
	Parameters           map[string]string `json:"Parameters,omitempty"`
}

type storageDescriptorJSON struct {
	Columns       []columnJSON      `json:"Columns,omitempty"`
	Location      string            `json:"Location,omitempty"`
	InputFormat   string            `json:"InputFormat,omitempty"`
	OutputFormat  string            `json:"OutputFormat,omitempty"`
	Compressed    bool              `json:"Compressed,omitempty"`
	SerdeInfo     *serDeInfoJSON    `json:"SerdeInfo,omitempty"`
	Parameters    map[string]string `json:"Parameters,omitempty"`
	BucketColumns []string          `json:"BucketColumns,omitempty"`
	SortColumns   []orderJSON       `json:"SortColumns,omitempty"`
}

func sdToWire(in *driver.StorageDescriptor) *storageDescriptorJSON {
	if in == nil {
		return nil
	}

	out := &storageDescriptorJSON{
		Columns: columnsToWire(in.Columns), Location: in.Location, InputFormat: in.InputFormat,
		OutputFormat: in.OutputFormat, Compressed: in.Compressed, Parameters: in.Parameters,
		BucketColumns: in.BucketColumns,
	}

	if in.SerdeInfo != nil {
		out.SerdeInfo = &serDeInfoJSON{
			Name: in.SerdeInfo.Name, SerializationLibrary: in.SerdeInfo.SerializationLibrary,
			Parameters: in.SerdeInfo.Parameters,
		}
	}

	for i := range in.SortColumns {
		out.SortColumns = append(out.SortColumns, orderJSON{
			Column: in.SortColumns[i].Column, SortOrder: in.SortColumns[i].SortOrder,
		})
	}

	return out
}

func sdFromWire(in *storageDescriptorJSON) *driver.StorageDescriptor {
	if in == nil {
		return nil
	}

	out := &driver.StorageDescriptor{
		Columns: columnsFromWire(in.Columns), Location: in.Location, InputFormat: in.InputFormat,
		OutputFormat: in.OutputFormat, Compressed: in.Compressed, Parameters: in.Parameters,
		BucketColumns: in.BucketColumns,
	}

	if in.SerdeInfo != nil {
		out.SerdeInfo = &driver.SerDeInfo{
			Name: in.SerdeInfo.Name, SerializationLibrary: in.SerdeInfo.SerializationLibrary,
			Parameters: in.SerdeInfo.Parameters,
		}
	}

	for i := range in.SortColumns {
		out.SortColumns = append(out.SortColumns, driver.Order{
			Column: in.SortColumns[i].Column, SortOrder: in.SortColumns[i].SortOrder,
		})
	}

	return out
}

// errorDetailJSON is the Glue ErrorDetail wire shape used in Batch* responses.
type errorDetailJSON struct {
	ErrorCode    string `json:"ErrorCode,omitempty"`
	ErrorMessage string `json:"ErrorMessage,omitempty"`
}

// --- database wire shapes ---

type databaseInputJSON struct {
	Name        string            `json:"Name"`
	Description string            `json:"Description,omitempty"`
	LocationURI string            `json:"LocationURI,omitempty"`
	Parameters  map[string]string `json:"Parameters,omitempty"`
}

type databaseJSON struct {
	CatalogID   string            `json:"CatalogID,omitempty"`
	Name        string            `json:"Name"`
	Description string            `json:"Description,omitempty"`
	LocationURI string            `json:"LocationURI,omitempty"`
	Parameters  map[string]string `json:"Parameters,omitempty"`
	CreateTime  *float64          `json:"CreateTime,omitempty"`
}

func dbFromInput(catalogID string, in databaseInputJSON) driver.Database {
	return driver.Database{
		CatalogID: catalogID, Name: in.Name, Description: in.Description,
		LocationURI: in.LocationURI, Parameters: in.Parameters,
	}
}

func dbToWire(d *driver.Database) databaseJSON {
	return databaseJSON{
		CatalogID: d.CatalogID, Name: d.Name, Description: d.Description,
		LocationURI: d.LocationURI, Parameters: d.Parameters, CreateTime: epochOrNil(d.CreateTime),
	}
}

func dbsToWire(in []driver.Database) []databaseJSON {
	out := make([]databaseJSON, 0, len(in))
	for i := range in {
		out = append(out, dbToWire(&in[i]))
	}

	return out
}

// --- table wire shapes ---

type tableInputJSON struct {
	Name              string                 `json:"Name"`
	Description       string                 `json:"Description,omitempty"`
	Owner             string                 `json:"Owner,omitempty"`
	TableType         string                 `json:"TableType,omitempty"`
	StorageDescriptor *storageDescriptorJSON `json:"StorageDescriptor,omitempty"`
	PartitionKeys     []columnJSON           `json:"PartitionKeys,omitempty"`
	Parameters        map[string]string      `json:"Parameters,omitempty"`
	ViewOriginalText  string                 `json:"ViewOriginalText,omitempty"`
	ViewExpandedText  string                 `json:"ViewExpandedText,omitempty"`
	Retention         int32                  `json:"Retention,omitempty"`
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func tableFromInput(catalogID, dbName string, in tableInputJSON) driver.Table {
	return driver.Table{
		CatalogID: catalogID, DatabaseName: dbName, Name: in.Name, Description: in.Description,
		Owner: in.Owner, TableType: in.TableType, StorageDescriptor: sdFromWire(in.StorageDescriptor),
		PartitionKeys: columnsFromWire(in.PartitionKeys), Parameters: in.Parameters,
		ViewOriginalText: in.ViewOriginalText, ViewExpandedText: in.ViewExpandedText, Retention: in.Retention,
	}
}

type tableJSON struct {
	CatalogID         string                 `json:"CatalogID,omitempty"`
	DatabaseName      string                 `json:"DatabaseName,omitempty"`
	Name              string                 `json:"Name"`
	Description       string                 `json:"Description,omitempty"`
	Owner             string                 `json:"Owner,omitempty"`
	TableType         string                 `json:"TableType,omitempty"`
	StorageDescriptor *storageDescriptorJSON `json:"StorageDescriptor,omitempty"`
	PartitionKeys     []columnJSON           `json:"PartitionKeys,omitempty"`
	Parameters        map[string]string      `json:"Parameters,omitempty"`
	ViewOriginalText  string                 `json:"ViewOriginalText,omitempty"`
	ViewExpandedText  string                 `json:"ViewExpandedText,omitempty"`
	CreateTime        *float64               `json:"CreateTime,omitempty"`
	UpdateTime        *float64               `json:"UpdateTime,omitempty"`
	Retention         int32                  `json:"Retention,omitempty"`
	VersionID         string                 `json:"VersionID,omitempty"`
}

func tableToWire(t *driver.Table) tableJSON {
	return tableJSON{
		CatalogID: t.CatalogID, DatabaseName: t.DatabaseName, Name: t.Name, Description: t.Description,
		Owner: t.Owner, TableType: t.TableType, StorageDescriptor: sdToWire(t.StorageDescriptor),
		PartitionKeys: columnsToWire(t.PartitionKeys), Parameters: t.Parameters,
		ViewOriginalText: t.ViewOriginalText, ViewExpandedText: t.ViewExpandedText,
		CreateTime: epochOrNil(t.CreateTime), UpdateTime: epochOrNil(t.UpdateTime),
		Retention: t.Retention, VersionID: t.VersionID,
	}
}

func tablesToWire(in []driver.Table) []tableJSON {
	out := make([]tableJSON, 0, len(in))
	for i := range in {
		out = append(out, tableToWire(&in[i]))
	}

	return out
}

// --- partition wire shapes ---

type partitionInputJSON struct {
	Values            []string               `json:"Values"`
	StorageDescriptor *storageDescriptorJSON `json:"StorageDescriptor,omitempty"`
	Parameters        map[string]string      `json:"Parameters,omitempty"`
}

func partFromInput(catalogID, dbName, tblName string, in partitionInputJSON) driver.Partition {
	return driver.Partition{
		CatalogID: catalogID, DatabaseName: dbName, TableName: tblName, Values: in.Values,
		StorageDescriptor: sdFromWire(in.StorageDescriptor), Parameters: in.Parameters,
	}
}

type partitionJSON struct {
	CatalogID         string                 `json:"CatalogID,omitempty"`
	DatabaseName      string                 `json:"DatabaseName,omitempty"`
	TableName         string                 `json:"TableName,omitempty"`
	Values            []string               `json:"Values"`
	StorageDescriptor *storageDescriptorJSON `json:"StorageDescriptor,omitempty"`
	Parameters        map[string]string      `json:"Parameters,omitempty"`
	CreationTime      *float64               `json:"CreationTime,omitempty"`
}

func partToWire(p *driver.Partition) partitionJSON {
	return partitionJSON{
		CatalogID: p.CatalogID, DatabaseName: p.DatabaseName, TableName: p.TableName, Values: p.Values,
		StorageDescriptor: sdToWire(p.StorageDescriptor), Parameters: p.Parameters,
		CreationTime: epochOrNil(p.CreationTime),
	}
}

func partsToWire(in []driver.Partition) []partitionJSON {
	out := make([]partitionJSON, 0, len(in))
	for i := range in {
		out = append(out, partToWire(&in[i]))
	}

	return out
}
