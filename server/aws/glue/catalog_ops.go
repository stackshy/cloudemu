package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// registerCatalogRoutes wires the Data Catalog resource operations.
func (h *Handler) registerCatalogRoutes() {
	h.routes["CreateDatabase"] = h.createDatabase
	h.routes["GetDatabase"] = h.getDatabase
	h.routes["UpdateDatabase"] = h.updateDatabase
	h.routes["DeleteDatabase"] = h.deleteDatabase
	h.routes["GetDatabases"] = h.getDatabases

	h.routes["CreateTable"] = h.createTable
	h.routes["GetTable"] = h.getTable
	h.routes["UpdateTable"] = h.updateTable
	h.routes["DeleteTable"] = h.deleteTable
	h.routes["GetTables"] = h.getTables
	h.routes["SearchTables"] = h.searchTables
	h.routes["BatchDeleteTable"] = h.batchDeleteTable

	h.routes["GetTableVersion"] = h.getTableVersion
	h.routes["GetTableVersions"] = h.getTableVersions
	h.routes["DeleteTableVersion"] = h.deleteTableVersion
	h.routes["BatchDeleteTableVersion"] = h.batchDeleteTableVersion

	h.routes["CreatePartition"] = h.createPartition
	h.routes["GetPartition"] = h.getPartition
	h.routes["UpdatePartition"] = h.updatePartition
	h.routes["DeletePartition"] = h.deletePartition
	h.routes["GetPartitions"] = h.getPartitions
	h.routes["BatchCreatePartition"] = h.batchCreatePartition
	h.routes["BatchDeletePartition"] = h.batchDeletePartition
	h.routes["BatchUpdatePartition"] = h.batchUpdatePartition
	h.routes["BatchGetPartition"] = h.batchGetPartition

	h.routes["CreateUserDefinedFunction"] = h.createUDF
	h.routes["GetUserDefinedFunction"] = h.getUDF
	h.routes["UpdateUserDefinedFunction"] = h.updateUDF
	h.routes["DeleteUserDefinedFunction"] = h.deleteUDF
	h.routes["GetUserDefinedFunctions"] = h.getUDFs

	h.routes["CreateConnection"] = h.createConnection
	h.routes["GetConnection"] = h.getConnection
	h.routes["UpdateConnection"] = h.updateConnection
	h.routes["DeleteConnection"] = h.deleteConnection
	h.routes["GetConnections"] = h.getConnections
	h.routes["BatchDeleteConnection"] = h.batchDeleteConnection
	h.routes["TestConnection"] = h.testConnection

	h.routes["CreateCatalog"] = h.createCatalog
	h.routes["GetCatalog"] = h.getCatalog
	h.routes["UpdateCatalog"] = h.updateCatalog
	h.routes["DeleteCatalog"] = h.deleteCatalog
	h.routes["GetCatalogs"] = h.getCatalogs

	h.routes["TagResource"] = h.tagResource
	h.routes["UntagResource"] = h.untagResource
	h.routes["GetTags"] = h.getTags
	h.routes["PutResourcePolicy"] = h.putResourcePolicy
	h.routes["GetResourcePolicy"] = h.getResourcePolicy
	h.routes["DeleteResourcePolicy"] = h.deleteResourcePolicy
	h.routes["PutDataCatalogEncryptionSettings"] = h.putEncryptionSettings
	h.routes["GetDataCatalogEncryptionSettings"] = h.getEncryptionSettings
}

// --- databases ---

type createDatabaseRequest struct {
	CatalogID     string            `json:"CatalogId"`
	DatabaseInput databaseInputJSON `json:"DatabaseInput"`
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDatabaseRequest) (any, error) {
		db := dbFromInput(req.CatalogID, req.DatabaseInput)
		if err := h.glue.CreateDatabase(ctx, req.CatalogID, db); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type nameCatalogRequest struct {
	CatalogID string `json:"CatalogId"`
	Name      string `json:"Name"`
}

type getDatabaseResponse struct {
	Database databaseJSON `json:"Database"`
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameCatalogRequest) (any, error) {
		db, err := h.glue.GetDatabase(ctx, req.CatalogID, req.Name)
		if err != nil {
			return nil, err
		}

		return getDatabaseResponse{Database: dbToWire(db)}, nil
	})
}

type updateDatabaseRequest struct {
	CatalogID     string            `json:"CatalogId"`
	Name          string            `json:"Name"`
	DatabaseInput databaseInputJSON `json:"DatabaseInput"`
}

func (h *Handler) updateDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateDatabaseRequest) (any, error) {
		db := dbFromInput(req.CatalogID, req.DatabaseInput)
		if err := h.glue.UpdateDatabase(ctx, req.CatalogID, req.Name, db); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *nameCatalogRequest) (any, error) {
		if err := h.glue.DeleteDatabase(ctx, req.CatalogID, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getDatabasesRequest struct {
	CatalogID  string `json:"CatalogId"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type getDatabasesResponse struct {
	DatabaseList []databaseJSON `json:"DatabaseList"`
	NextToken    string         `json:"NextToken,omitempty"`
}

func (h *Handler) getDatabases(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getDatabasesRequest) (any, error) {
		dbs, next, err := h.glue.GetDatabases(ctx, req.CatalogID,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return getDatabasesResponse{DatabaseList: dbsToWire(dbs), NextToken: next}, nil
	})
}
