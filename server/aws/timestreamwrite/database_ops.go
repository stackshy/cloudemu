package timestreamwrite

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// registerDatabaseRoutes wires the database operations.
func (h *Handler) registerDatabaseRoutes() {
	h.routes["CreateDatabase"] = h.createDatabase
	h.routes["DescribeDatabase"] = h.describeDatabase
	h.routes["UpdateDatabase"] = h.updateDatabase
	h.routes["DeleteDatabase"] = h.deleteDatabase
	h.routes["ListDatabases"] = h.listDatabases
}

type createDatabaseRequest struct {
	DatabaseName string    `json:"DatabaseName"`
	KmsKeyID     string    `json:"KmsKeyId"`
	Tags         []tagJSON `json:"Tags"`
}

type databaseResponse struct {
	Database databaseJSON `json:"Database"`
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDatabaseRequest) (any, error) {
		db, err := h.timestream.CreateDatabase(ctx, &driver.CreateDatabaseInput{
			DatabaseName: req.DatabaseName,
			KmsKeyID:     req.KmsKeyID,
			Tags:         tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return databaseResponse{Database: toDatabase(db)}, nil
	})
}

type describeDatabaseRequest struct {
	DatabaseName string `json:"DatabaseName"`
}

func (h *Handler) describeDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeDatabaseRequest) (any, error) {
		db, err := h.timestream.DescribeDatabase(ctx, req.DatabaseName)
		if err != nil {
			return nil, err
		}

		return databaseResponse{Database: toDatabase(db)}, nil
	})
}

type updateDatabaseRequest struct {
	DatabaseName string `json:"DatabaseName"`
	KmsKeyID     string `json:"KmsKeyId"`
}

func (h *Handler) updateDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateDatabaseRequest) (any, error) {
		db, err := h.timestream.UpdateDatabase(ctx, &driver.UpdateDatabaseInput{
			DatabaseName: req.DatabaseName,
			KmsKeyID:     req.KmsKeyID,
		})
		if err != nil {
			return nil, err
		}

		return databaseResponse{Database: toDatabase(db)}, nil
	})
}

type deleteDatabaseRequest struct {
	DatabaseName string `json:"DatabaseName"`
}

func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteDatabaseRequest) (any, error) {
		if err := h.timestream.DeleteDatabase(ctx, req.DatabaseName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listDatabasesRequest struct {
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listDatabasesResponse struct {
	Databases []databaseJSON `json:"Databases"`
	NextToken string         `json:"NextToken,omitempty"`
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listDatabasesRequest) (any, error) {
		databases, next, err := h.timestream.ListDatabases(ctx, driver.Page{
			NextToken: req.NextToken, MaxResults: req.MaxResults,
		})
		if err != nil {
			return nil, err
		}

		resp := listDatabasesResponse{
			Databases: make([]databaseJSON, 0, len(databases)),
			NextToken: next,
		}
		for i := range databases {
			resp.Databases = append(resp.Databases, toDatabase(&databases[i]))
		}

		return resp, nil
	})
}
