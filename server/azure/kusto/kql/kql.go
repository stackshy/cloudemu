// Package kql holds the pure, dependency-free data model for the Azure Data
// Explorer (Kusto) query data plane: typed columns, a row-oriented table value,
// and the Kusto scalar-type mapping shared by the control-command dispatcher and
// the wire-frame encoders.
//
// It is the Azure-only analog of services/database/driver/expr — a standalone
// stdlib library its one consumer (server/azure/kusto) can test in isolation.
// This first increment carries only the data model and schema parsing; the KQL
// lexer, recursive-descent parser and pipeline evaluator (where/project/
// summarize/...) land in a later increment and build on the Table value defined
// here.
package kql

import "strings"

// ColumnType is a Kusto scalar column type, spelled with its canonical Kusto
// name (the value that appears as ColumnType in wire frames).
type ColumnType string

// The Kusto scalar types. Phase 1 operates richly on the first six; the
// remainder are stored and echoed but not yet computed over.
const (
	TypeString   ColumnType = "string"
	TypeLong     ColumnType = "long"
	TypeInt      ColumnType = "int"
	TypeReal     ColumnType = "real"
	TypeBool     ColumnType = "bool"
	TypeDateTime ColumnType = "datetime"
	TypeTimespan ColumnType = "timespan"
	TypeDynamic  ColumnType = "dynamic"
	TypeGUID     ColumnType = "guid"
	TypeDecimal  ColumnType = "decimal"
)

// Column is one ordered, typed column of a table schema.
type Column struct {
	Name string
	Type ColumnType
}

// Table is a row-oriented, schema-carrying result. Each row in Rows is aligned
// positionally to Columns; a cell's Go type follows the column's ColumnType
// (string, int64 for long, int32 for int, float64 for real, bool, time.Time for
// datetime, and the raw JSON value otherwise). A row-oriented model is the
// cheapest fit for KQL's ordered, typed pipeline and keeps the future
// project/extend/summarize operators trivial; a columnar layout is a possible
// later optimization only.
type Table struct {
	Name    string
	Columns []Column
	Rows    [][]any
}

// dotNetString is the .NET type name for a Kusto string column; it is also the
// DataType() fallback for an unrecognized ColumnType.
const dotNetString = "String"

// ParseColumnType maps a Kusto scalar type name — or one of the .NET / CLR
// aliases real Kusto accepts (Int64, System.String, ...) — to its canonical
// ColumnType. ok is false for an unknown type.
func ParseColumnType(name string) (ColumnType, bool) {
	aliases := map[string]ColumnType{
		"string": TypeString, "system.string": TypeString,
		"long": TypeLong, "int64": TypeLong, "system.int64": TypeLong,
		"int": TypeInt, "int32": TypeInt, "system.int32": TypeInt,
		"real": TypeReal, "double": TypeReal, "system.double": TypeReal,
		"bool": TypeBool, "boolean": TypeBool, "system.boolean": TypeBool,
		"datetime": TypeDateTime, "date": TypeDateTime, "system.datetime": TypeDateTime,
		"timespan": TypeTimespan, "time": TypeTimespan, "system.timespan": TypeTimespan,
		"dynamic": TypeDynamic, "object": TypeDynamic, "system.object": TypeDynamic,
		"guid": TypeGUID, "uuid": TypeGUID, "system.guid": TypeGUID,
		"decimal": TypeDecimal, "sqldecimal": TypeDecimal, "system.data.sqltypes.sqldecimal": TypeDecimal,
	}

	t, ok := aliases[strings.ToLower(strings.TrimSpace(name))]

	return t, ok
}

// DataType returns the .NET-style type name Kusto reports alongside ColumnType
// in v1 result frames (e.g. "Int64" for long, "String" for string).
func (t ColumnType) DataType() string {
	names := map[ColumnType]string{
		TypeString:   dotNetString,
		TypeLong:     "Int64",
		TypeInt:      "Int32",
		TypeReal:     "Double",
		TypeBool:     "Boolean",
		TypeDateTime: "DateTime",
		TypeTimespan: "TimeSpan",
		TypeDynamic:  "Object",
		TypeGUID:     "Guid",
		TypeDecimal:  "SqlDecimal",
	}

	if n, ok := names[t]; ok {
		return n
	}

	return dotNetString
}

// CSLSchema renders columns as the comma-separated "name:type" CSL schema string
// Kusto uses in .create table and .show table cslschema (e.g. "id:long,name:string").
func CSLSchema(cols []Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c.Name + ":" + string(c.Type)
	}

	return strings.Join(parts, ",")
}
