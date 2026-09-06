package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type getDatabaseRequest struct {
	CatalogName  string `json:"CatalogName"`
	DatabaseName string `json:"DatabaseName"`
}

type getDatabaseResponse struct {
	Database databaseJSON `json:"Database"`
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getDatabaseRequest) (any, error) {
		db, err := h.athena.GetDatabase(ctx, req.CatalogName, req.DatabaseName)
		if err != nil {
			return nil, err
		}

		return getDatabaseResponse{Database: databaseToWire(db)}, nil
	})
}

type listDatabasesRequest struct {
	CatalogName string `json:"CatalogName"`
	NextToken   string `json:"NextToken"`
	MaxResults  int32  `json:"MaxResults"`
}

type listDatabasesResponse struct {
	DatabaseList []databaseJSON `json:"DatabaseList"`
	NextToken    string         `json:"NextToken,omitempty"`
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listDatabasesRequest) (any, error) {
		dbs, next, err := h.athena.ListDatabases(ctx, req.CatalogName,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]databaseJSON, 0, len(dbs))
		for i := range dbs {
			out = append(out, databaseToWire(&dbs[i]))
		}

		return listDatabasesResponse{DatabaseList: out, NextToken: next}, nil
	})
}

type getDataCatalogRequest struct {
	Name string `json:"Name"`
}

type getDataCatalogResponse struct {
	DataCatalog dataCatalogJSON `json:"DataCatalog"`
}

func (h *Handler) getDataCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getDataCatalogRequest) (any, error) {
		dc, err := h.athena.GetDataCatalog(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getDataCatalogResponse{DataCatalog: dataCatalogToWire(dc)}, nil
	})
}

type listDataCatalogsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listDataCatalogsResponse struct {
	DataCatalogsSummary []dataCatalogSummaryJSON `json:"DataCatalogsSummary"`
	NextToken           string                   `json:"NextToken,omitempty"`
}

func (h *Handler) listDataCatalogs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listDataCatalogsRequest) (any, error) {
		cats, next, err := h.athena.ListDataCatalogs(ctx,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]dataCatalogSummaryJSON, 0, len(cats))
		for _, c := range cats {
			out = append(out, dataCatalogSummaryJSON{CatalogName: c.CatalogName, Type: c.Type})
		}

		return listDataCatalogsResponse{DataCatalogsSummary: out, NextToken: next}, nil
	})
}
