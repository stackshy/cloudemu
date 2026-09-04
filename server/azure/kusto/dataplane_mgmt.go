package kusto

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/azure/kusto/kql"
)

// serveMgmt handles a /v1/rest/mgmt control command. The leading dot is the
// discriminator: a csl that does not begin with "." is not a control command.
func (h *DataPlaneHandler) serveMgmt(w http.ResponseWriter, cluster string, req dataRequest) {
	csl := strings.TrimSpace(req.CSL)
	if !strings.HasPrefix(csl, ".") {
		writeDataError(w, http.StatusBadRequest, "BadRequest", "a control command (csl) must begin with '.'")
		return
	}

	store := h.data.storeFor(cluster, req.DB)

	tables, err := execMgmt(store, req.DB, csl)
	if err != nil {
		writeMgmtError(w, err)
		return
	}

	writeV1(w, tables)
}

// execMgmt dispatches a control command to its handler. The prefix checks are
// ordered so a longer keyword (create-merge table, show tables) is matched
// before its shorter sibling.
func execMgmt(store *tableStore, db, csl string) ([]kql.Table, error) {
	body := strings.TrimSpace(strings.TrimPrefix(csl, "."))
	lower := strings.ToLower(body)

	switch {
	case hasCmd(lower, "create-merge table"):
		return createTableCmd(store, db, body[len("create-merge table"):], true)
	case hasCmd(lower, "create table"):
		return createTableCmd(store, db, body[len("create table"):], false)
	case hasCmd(lower, "drop table"):
		return dropTableCmd(store, body[len("drop table"):])
	case hasCmd(lower, "show tables"):
		return showTablesCmd(store, db), nil
	case hasCmd(lower, "show table"):
		return showTableSchemaCmd(store, db, body[len("show table"):])
	case hasCmd(lower, "show database"):
		return showDatabaseSchemaCmd(store, db, body[len("show database"):])
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported control command: %s", csl)
	}
}

// hasCmd reports whether lower is exactly kw or begins with "kw " — so
// "show table" matches "show table Events schema" but not "show tables".
func hasCmd(lower, kw string) bool {
	return lower == kw || strings.HasPrefix(lower, kw+" ") || strings.HasPrefix(lower, kw+"(")
}

// createTableCmd registers a table from the "T (col:type, ...)" tail of a
// .create table / .create-merge table command and echoes the created schema.
func createTableCmd(store *tableStore, db, tail string, merge bool) ([]kql.Table, error) {
	tail = strings.TrimSpace(tail)

	open := strings.IndexByte(tail, '(')
	if open < 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "create table: expected a (column:type, ...) list")
	}

	name := unquoteName(strings.TrimSpace(tail[:open]))
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "create table: missing table name")
	}

	cols, err := kql.ParseColumnList(tail[open:])
	if err != nil {
		return nil, err
	}

	t, err := store.createTable(name, cols, merge)
	if err != nil {
		return nil, err
	}

	cols = t.Columns

	rows := [][]any{{t.Name, kql.CSLSchema(cols), db}}

	return resultTable([]kql.Column{
		{Name: "TableName", Type: kql.TypeString},
		{Name: "Schema", Type: kql.TypeString},
		{Name: "DatabaseName", Type: kql.TypeString},
	}, rows), nil
}

// dropTableCmd removes a table named in the tail of ".drop table T [ifexists]"
// and acks with the dropped name.
func dropTableCmd(store *tableStore, tail string) ([]kql.Table, error) {
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "drop table: missing table name")
	}

	name := unquoteName(fields[0])
	ifExists := false

	for _, f := range fields[1:] {
		if strings.EqualFold(f, "ifexists") {
			ifExists = true
		}
	}

	if err := store.dropTable(name, ifExists); err != nil {
		return nil, err
	}

	return resultTable(
		[]kql.Column{{Name: "TableName", Type: kql.TypeString}},
		[][]any{{name}},
	), nil
}

// showTablesCmd lists every table in the database.
func showTablesCmd(store *tableStore, db string) []kql.Table {
	tables := store.listTables()
	rows := make([][]any, 0, len(tables))

	for _, t := range tables {
		rows = append(rows, []any{t.Name, db, "", ""})
	}

	return resultTable([]kql.Column{
		{Name: "TableName", Type: kql.TypeString},
		{Name: "DatabaseName", Type: kql.TypeString},
		{Name: "Folder", Type: kql.TypeString},
		{Name: "DocString", Type: kql.TypeString},
	}, rows)
}

// showTableSchemaCmd returns one table's schema for ".show table T schema" (a
// JSON schema) or ".show table T cslschema" (the CSL schema string).
func showTableSchemaCmd(store *tableStore, db, tail string) ([]kql.Table, error) {
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "show table: missing table name")
	}

	name := unquoteName(fields[0])

	t, ok := store.getTable(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "table not found: %s", name)
	}

	cslMode := len(fields) >= 2 && strings.EqualFold(fields[1], "cslschema")

	schema := jsonTableSchema(t)
	if cslMode {
		schema = kql.CSLSchema(t.Columns)
	}

	return resultTable([]kql.Column{
		{Name: "TableName", Type: kql.TypeString},
		{Name: "Schema", Type: kql.TypeString},
		{Name: "DatabaseName", Type: kql.TypeString},
		{Name: "Folder", Type: kql.TypeString},
		{Name: "DocString", Type: kql.TypeString},
	}, [][]any{{t.Name, schema, db, "", ""}}), nil
}

// showDatabaseSchemaCmd returns the whole-database schema, tabular by default or
// as a single JSON string when the command ends with "as json".
func showDatabaseSchemaCmd(store *tableStore, db, tail string) ([]kql.Table, error) {
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(tail)), "as json") {
		return databaseSchemaJSON(store, db), nil
	}

	return databaseSchemaTabular(store, db), nil
}

func databaseSchemaTabular(store *tableStore, db string) []kql.Table {
	rows := make([][]any, 0)

	for _, t := range store.listTables() {
		for _, c := range t.Columns {
			rows = append(rows, []any{db, t.Name, c.Name, string(c.Type)})
		}
	}

	return resultTable([]kql.Column{
		{Name: "DatabaseName", Type: kql.TypeString},
		{Name: "TableName", Type: kql.TypeString},
		{Name: "ColumnName", Type: kql.TypeString},
		{Name: "ColumnType", Type: kql.TypeString},
	}, rows)
}

func databaseSchemaJSON(store *tableStore, db string) []kql.Table {
	schema := dbSchema{Name: db, Tables: map[string]tableSchema{}}
	for _, t := range store.listTables() {
		schema.Tables[t.Name] = buildTableSchema(t)
	}

	blob, _ := json.Marshal(schema)

	return resultTable(
		[]kql.Column{{Name: "DatabaseSchema", Type: kql.TypeString}},
		[][]any{{string(blob)}},
	)
}

// dbSchema / tableSchema / schemaColumn mirror the shape Kusto emits for a JSON
// database or table schema.
type dbSchema struct {
	Name   string                 `json:"Name"`
	Tables map[string]tableSchema `json:"Tables"`
}

type tableSchema struct {
	Name           string         `json:"Name"`
	OrderedColumns []schemaColumn `json:"OrderedColumns"`
}

type schemaColumn struct {
	Name    string `json:"Name"`
	Type    string `json:"Type"`
	CslType string `json:"CslType"`
}

func buildTableSchema(t *kql.Table) tableSchema {
	cols := make([]schemaColumn, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = schemaColumn{Name: c.Name, Type: "System." + c.Type.DataType(), CslType: string(c.Type)}
	}

	return tableSchema{Name: t.Name, OrderedColumns: cols}
}

func jsonTableSchema(t *kql.Table) string {
	blob, _ := json.Marshal(buildTableSchema(t))

	return string(blob)
}

// resultTable wraps a single result table (the only shape a control command
// returns) as the one-element slice the v1 encoder expects.
func resultTable(cols []kql.Column, rows [][]any) []kql.Table {
	return []kql.Table{{Columns: cols, Rows: rows}}
}

// unquoteName strips the brackets, quotes and backticks Kusto tolerates around
// an entity name (e.g. ['My Table'], "T", `T`).
func unquoteName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "[")
	name = strings.TrimSuffix(name, "]")
	name = strings.Trim(name, "'\"`")

	return strings.TrimSpace(name)
}

// writeMgmtError maps a canonical error to the matching Kusto data-plane HTTP
// status and OneApiErrors body.
func writeMgmtError(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeDataError(w, http.StatusNotFound, "EntityNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeDataError(w, http.StatusConflict, "EntityAlreadyExists", err.Error())
	default:
		writeDataError(w, http.StatusBadRequest, "BadRequest", err.Error())
	}
}
