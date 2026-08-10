package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type createTableRequest struct {
	CatalogID    string         `json:"CatalogId"`
	DatabaseName string         `json:"DatabaseName"`
	TableInput   tableInputJSON `json:"TableInput"`
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createTableRequest) (any, error) {
		tbl := tableFromInput(req.CatalogID, req.DatabaseName, req.TableInput)
		if err := h.glue.CreateTable(ctx, req.CatalogID, req.DatabaseName, tbl); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getTableRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	Name         string `json:"Name"`
}

type getTableResponse struct {
	Table tableJSON `json:"Table"`
}

func (h *Handler) getTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTableRequest) (any, error) {
		tbl, err := h.glue.GetTable(ctx, req.CatalogID, req.DatabaseName, req.Name)
		if err != nil {
			return nil, err
		}

		return getTableResponse{Table: tableToWire(tbl)}, nil
	})
}

type updateTableRequest struct {
	CatalogID    string         `json:"CatalogId"`
	DatabaseName string         `json:"DatabaseName"`
	TableInput   tableInputJSON `json:"TableInput"`
}

func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateTableRequest) (any, error) {
		tbl := tableFromInput(req.CatalogID, req.DatabaseName, req.TableInput)
		if err := h.glue.UpdateTable(ctx, req.CatalogID, req.DatabaseName, tbl); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTableRequest) (any, error) {
		if err := h.glue.DeleteTable(ctx, req.CatalogID, req.DatabaseName, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getTablesRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	NextToken    string `json:"NextToken"`
	MaxResults   int32  `json:"MaxResults"`
}

type getTablesResponse struct {
	TableList []tableJSON `json:"TableList"`
	NextToken string      `json:"NextToken,omitempty"`
}

func (h *Handler) getTables(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTablesRequest) (any, error) {
		tbls, next, err := h.glue.GetTables(ctx, req.CatalogID, req.DatabaseName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return getTablesResponse{TableList: tablesToWire(tbls), NextToken: next}, nil
	})
}

type searchTablesRequest struct {
	CatalogID  string `json:"CatalogId"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

func (h *Handler) searchTables(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *searchTablesRequest) (any, error) {
		tbls, next, err := h.glue.SearchTables(ctx, req.CatalogID,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return getTablesResponse{TableList: tablesToWire(tbls), NextToken: next}, nil
	})
}

type batchDeleteTableRequest struct {
	CatalogID      string   `json:"CatalogId"`
	DatabaseName   string   `json:"DatabaseName"`
	TablesToDelete []string `json:"TablesToDelete"`
}

type tableErrorJSON struct {
	TableName   string           `json:"TableName,omitempty"`
	ErrorDetail *errorDetailJSON `json:"ErrorDetail,omitempty"`
}

type batchDeleteTableResponse struct {
	Errors []tableErrorJSON `json:"Errors,omitempty"`
}

func (h *Handler) batchDeleteTable(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchDeleteTableRequest) (any, error) {
		errs, err := h.glue.BatchDeleteTable(ctx, req.CatalogID, req.DatabaseName, req.TablesToDelete)
		if err != nil {
			return nil, err
		}

		out := make([]tableErrorJSON, 0, len(errs))
		for i := range errs {
			out = append(out, tableErrorJSON{
				TableName:   errs[i].Name,
				ErrorDetail: &errorDetailJSON{ErrorCode: errs[i].ErrorCode, ErrorMessage: errs[i].ErrorMessage},
			})
		}

		return batchDeleteTableResponse{Errors: out}, nil
	})
}

// --- table versions ---

type tableVersionJSON struct {
	Table     tableJSON `json:"Table"`
	VersionID string    `json:"VersionID,omitempty"`
}

type getTableVersionRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
	VersionID    string `json:"VersionId"`
}

type getTableVersionResponse struct {
	TableVersion tableVersionJSON `json:"TableVersion"`
}

func (h *Handler) getTableVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTableVersionRequest) (any, error) {
		tv, err := h.glue.GetTableVersion(ctx, req.CatalogID, req.DatabaseName, req.TableName, req.VersionID)
		if err != nil {
			return nil, err
		}

		return getTableVersionResponse{
			TableVersion: tableVersionJSON{Table: tableToWire(&tv.Table), VersionID: tv.VersionID},
		}, nil
	})
}

type getTableVersionsRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
	NextToken    string `json:"NextToken"`
	MaxResults   int32  `json:"MaxResults"`
}

type getTableVersionsResponse struct {
	TableVersions []tableVersionJSON `json:"TableVersions"`
	NextToken     string             `json:"NextToken,omitempty"`
}

func (h *Handler) getTableVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getTableVersionsRequest) (any, error) {
		tvs, next, err := h.glue.GetTableVersions(ctx, req.CatalogID, req.DatabaseName, req.TableName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]tableVersionJSON, 0, len(tvs))
		for i := range tvs {
			out = append(out, tableVersionJSON{Table: tableToWire(&tvs[i].Table), VersionID: tvs[i].VersionID})
		}

		return getTableVersionsResponse{TableVersions: out, NextToken: next}, nil
	})
}

type deleteTableVersionRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	TableName    string `json:"TableName"`
	VersionID    string `json:"VersionId"`
}

func (h *Handler) deleteTableVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteTableVersionRequest) (any, error) {
		if err := h.glue.DeleteTableVersion(ctx, req.CatalogID, req.DatabaseName,
			req.TableName, req.VersionID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type batchDeleteTableVersionRequest struct {
	CatalogID    string   `json:"CatalogId"`
	DatabaseName string   `json:"DatabaseName"`
	TableName    string   `json:"TableName"`
	VersionIDs   []string `json:"VersionIds"`
}

type batchDeleteTableVersionResponse struct {
	Errors []tableVersionErrorJSON `json:"Errors,omitempty"`
}

type tableVersionErrorJSON struct {
	TableName   string           `json:"TableName,omitempty"`
	VersionID   string           `json:"VersionID,omitempty"`
	ErrorDetail *errorDetailJSON `json:"ErrorDetail,omitempty"`
}

func (h *Handler) batchDeleteTableVersion(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchDeleteTableVersionRequest) (any, error) {
		errs, err := h.glue.BatchDeleteTableVersion(ctx, req.CatalogID, req.DatabaseName,
			req.TableName, req.VersionIDs)
		if err != nil {
			return nil, err
		}

		out := make([]tableVersionErrorJSON, 0, len(errs))

		for i := range errs {
			vid := ""
			if len(errs[i].Values) > 0 {
				vid = errs[i].Values[0]
			}

			out = append(out, tableVersionErrorJSON{
				TableName: req.TableName, VersionID: vid,
				ErrorDetail: &errorDetailJSON{ErrorCode: errs[i].ErrorCode, ErrorMessage: errs[i].ErrorMessage},
			})
		}

		return batchDeleteTableVersionResponse{Errors: out}, nil
	})
}
