package timestreamwrite

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// registerTableRoutes wires the table operations.
func (h *Handler) registerTableRoutes() {
	h.routes["CreateTable"] = h.createTable
	h.routes["DescribeTable"] = h.describeTable
	h.routes["UpdateTable"] = h.updateTable
	h.routes["DeleteTable"] = h.deleteTable
	h.routes["ListTables"] = h.listTables
}

type createTableRequest struct {
	DatabaseName                 string          `json:"DatabaseName"`
	TableName                    string          `json:"TableName"`
	RetentionProperties          *retentionJSON  `json:"RetentionProperties"`
	MagneticStoreWriteProperties json.RawMessage `json:"MagneticStoreWriteProperties"`
	Schema                       json.RawMessage `json:"Schema"`
	Tags                         []tagJSON       `json:"Tags"`
}

type tableResponse struct {
	Table tableJSON `json:"Table"`
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createTableRequest) (any, error) {
		table, err := h.timestream.CreateTable(ctx, &driver.CreateTableInput{
			DatabaseName:                 req.DatabaseName,
			TableName:                    req.TableName,
			RetentionProperties:          retentionFromWire(req.RetentionProperties),
			MagneticStoreWriteProperties: req.MagneticStoreWriteProperties,
			Schema:                       req.Schema,
			Tags:                         tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return tableResponse{Table: toTable(table)}, nil
	})
}

type describeTableRequest struct {
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
}

func (h *Handler) describeTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeTableRequest) (any, error) {
		table, err := h.timestream.DescribeTable(ctx, req.DatabaseName, req.TableName)
		if err != nil {
			return nil, err
		}

		return tableResponse{Table: toTable(table)}, nil
	})
}

type updateTableRequest struct {
	DatabaseName                 string          `json:"DatabaseName"`
	TableName                    string          `json:"TableName"`
	RetentionProperties          *retentionJSON  `json:"RetentionProperties"`
	MagneticStoreWriteProperties json.RawMessage `json:"MagneticStoreWriteProperties"`
	Schema                       json.RawMessage `json:"Schema"`
}

func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateTableRequest) (any, error) {
		table, err := h.timestream.UpdateTable(ctx, &driver.UpdateTableInput{
			DatabaseName:                 req.DatabaseName,
			TableName:                    req.TableName,
			RetentionProperties:          retentionFromWire(req.RetentionProperties),
			MagneticStoreWriteProperties: req.MagneticStoreWriteProperties,
			Schema:                       req.Schema,
		})
		if err != nil {
			return nil, err
		}

		return tableResponse{Table: toTable(table)}, nil
	})
}

type deleteTableRequest struct {
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteTableRequest) (any, error) {
		if err := h.timestream.DeleteTable(ctx, req.DatabaseName, req.TableName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTablesRequest struct {
	DatabaseName string `json:"DatabaseName"`
	MaxResults   int32  `json:"MaxResults"`
	NextToken    string `json:"NextToken"`
}

type listTablesResponse struct {
	Tables    []tableJSON `json:"Tables"`
	NextToken string      `json:"NextToken,omitempty"`
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTablesRequest) (any, error) {
		tables, next, err := h.timestream.ListTables(ctx, req.DatabaseName, driver.Page{
			NextToken: req.NextToken, MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}

		resp := listTablesResponse{
			Tables:    make([]tableJSON, 0, len(tables)),
			NextToken: next,
		}
		for i := range tables {
			resp.Tables = append(resp.Tables, toTable(&tables[i]))
		}

		return resp, nil
	})
}
