package kusto

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/azure/kusto/kql"
)

// dataContentType is the JSON content type every Kusto data-plane response uses.
const dataContentType = "application/json; charset=utf-8"

// v1Response is the single-object body the v1 REST surface returns:
// {"Tables":[{TableName, Columns, Rows}]}. Kusto .mgmt control commands always
// answer in this shape. Each column carries both the .NET-style DataType and the
// Kusto ColumnType, which the azure-kusto-go v1 decoder accepts.
type v1Response struct {
	Tables []v1Table `json:"Tables"`
}

type v1Table struct {
	TableName string     `json:"TableName"`
	Columns   []v1Column `json:"Columns"`
	Rows      [][]any    `json:"Rows"`
}

type v1Column struct {
	ColumnName string `json:"ColumnName"`
	DataType   string `json:"DataType"`
	ColumnType string `json:"ColumnType"`
}

// encodeV1 renders result tables into the v1 {Tables:[...]} envelope. A Kusto v1
// table name defaults to Table_<ordinal> when the result carries none.
func encodeV1(tables []kql.Table) v1Response {
	out := v1Response{Tables: make([]v1Table, 0, len(tables))}

	for i, t := range tables {
		name := t.Name
		if name == "" {
			name = "Table_" + strconv.Itoa(i)
		}

		out.Tables = append(out.Tables, v1Table{
			TableName: name,
			Columns:   encodeColumns(t.Columns),
			Rows:      normalizeRows(t.Rows),
		})
	}

	return out
}

func encodeColumns(cols []kql.Column) []v1Column {
	out := make([]v1Column, len(cols))
	for i, c := range cols {
		out[i] = v1Column{ColumnName: c.Name, DataType: c.Type.DataType(), ColumnType: string(c.Type)}
	}

	return out
}

// normalizeRows guarantees a non-nil, JSON-array-shaped Rows value so an empty
// result encodes as "Rows":[] rather than "Rows":null.
func normalizeRows(rows [][]any) [][]any {
	if rows == nil {
		return [][]any{}
	}

	return rows
}

// writeV1 writes result tables as a v1 response with a 200 status.
func writeV1(w http.ResponseWriter, tables []kql.Table) {
	writeJSON(w, http.StatusOK, encodeV1(tables))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", dataContentType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}

// oneAPIErrorResponse is the OneApiErrors error envelope the Kusto data plane
// returns on an HTTP error status; the azure-kusto-go client surfaces its
// message to the caller.
type oneAPIErrorResponse struct {
	Error oneAPIError `json:"error"`
}

type oneAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeDataError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, oneAPIErrorResponse{Error: oneAPIError{Code: code, Message: message}})
}
