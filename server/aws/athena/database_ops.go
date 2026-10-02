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
