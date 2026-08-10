package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// --- user-defined functions ---

type resourceURIJSON struct {
	ResourceType string `json:"ResourceType,omitempty"`
	URI          string `json:"URI,omitempty"`
}

type udfInputJSON struct {
	FunctionName string            `json:"FunctionName"`
	ClassName    string            `json:"ClassName,omitempty"`
	OwnerName    string            `json:"OwnerName,omitempty"`
	OwnerType    string            `json:"OwnerType,omitempty"`
	ResourceURIs []resourceURIJSON `json:"ResourceURIs,omitempty"`
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func udfFromInput(dbName string, in udfInputJSON) driver.UserDefinedFunction {
	uris := make([]driver.ResourceURI, 0, len(in.ResourceURIs))
	for i := range in.ResourceURIs {
		uris = append(uris, driver.ResourceURI{ResourceType: in.ResourceURIs[i].ResourceType, URI: in.ResourceURIs[i].URI})
	}

	return driver.UserDefinedFunction{
		DatabaseName: dbName, Name: in.FunctionName, ClassName: in.ClassName,
		OwnerName: in.OwnerName, OwnerType: in.OwnerType, ResourceURIs: uris,
	}
}

type udfJSON struct {
	FunctionName string            `json:"FunctionName"`
	DatabaseName string            `json:"DatabaseName,omitempty"`
	CatalogID    string            `json:"CatalogID,omitempty"`
	ClassName    string            `json:"ClassName,omitempty"`
	OwnerName    string            `json:"OwnerName,omitempty"`
	OwnerType    string            `json:"OwnerType,omitempty"`
	ResourceURIs []resourceURIJSON `json:"ResourceURIs,omitempty"`
	CreateTime   *float64          `json:"CreateTime,omitempty"`
}

func udfToWire(f *driver.UserDefinedFunction) udfJSON {
	uris := make([]resourceURIJSON, 0, len(f.ResourceURIs))
	for i := range f.ResourceURIs {
		uris = append(uris, resourceURIJSON{ResourceType: f.ResourceURIs[i].ResourceType, URI: f.ResourceURIs[i].URI})
	}

	return udfJSON{
		FunctionName: f.Name, DatabaseName: f.DatabaseName, CatalogID: f.CatalogID, ClassName: f.ClassName,
		OwnerName: f.OwnerName, OwnerType: f.OwnerType, ResourceURIs: uris, CreateTime: epochOrNil(f.CreateTime),
	}
}

type createUDFRequest struct {
	CatalogID     string       `json:"CatalogId"`
	DatabaseName  string       `json:"DatabaseName"`
	FunctionInput udfInputJSON `json:"FunctionInput"`
}

func (h *Handler) createUDF(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createUDFRequest) (any, error) {
		fn := udfFromInput(req.DatabaseName, req.FunctionInput)
		if err := h.glue.CreateUserDefinedFunction(ctx, req.CatalogID, req.DatabaseName, fn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type udfNameRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	FunctionName string `json:"FunctionName"`
}

type getUDFResponse struct {
	UserDefinedFunction udfJSON `json:"UserDefinedFunction"`
}

func (h *Handler) getUDF(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *udfNameRequest) (any, error) {
		fn, err := h.glue.GetUserDefinedFunction(ctx, req.CatalogID, req.DatabaseName, req.FunctionName)
		if err != nil {
			return nil, err
		}

		return getUDFResponse{UserDefinedFunction: udfToWire(fn)}, nil
	})
}

type updateUDFRequest struct {
	CatalogID     string       `json:"CatalogId"`
	DatabaseName  string       `json:"DatabaseName"`
	FunctionName  string       `json:"FunctionName"`
	FunctionInput udfInputJSON `json:"FunctionInput"`
}

func (h *Handler) updateUDF(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateUDFRequest) (any, error) {
		fn := udfFromInput(req.DatabaseName, req.FunctionInput)
		if err := h.glue.UpdateUserDefinedFunction(ctx, req.CatalogID, req.DatabaseName,
			req.FunctionName, fn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteUDF(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *udfNameRequest) (any, error) {
		if err := h.glue.DeleteUserDefinedFunction(ctx, req.CatalogID, req.DatabaseName,
			req.FunctionName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getUDFsRequest struct {
	CatalogID    string `json:"CatalogId"`
	DatabaseName string `json:"DatabaseName"`
	NextToken    string `json:"NextToken"`
	MaxResults   int32  `json:"MaxResults"`
}

type getUDFsResponse struct {
	UserDefinedFunctions []udfJSON `json:"UserDefinedFunctions"`
	NextToken            string    `json:"NextToken,omitempty"`
}

func (h *Handler) getUDFs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getUDFsRequest) (any, error) {
		fns, next, err := h.glue.GetUserDefinedFunctions(ctx, req.CatalogID, req.DatabaseName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]udfJSON, 0, len(fns))
		for i := range fns {
			out = append(out, udfToWire(&fns[i]))
		}

		return getUDFsResponse{UserDefinedFunctions: out, NextToken: next}, nil
	})
}

// --- connections ---

type connectionInputJSON struct {
	Name                 string            `json:"Name"`
	Description          string            `json:"Description,omitempty"`
	ConnectionType       string            `json:"ConnectionType,omitempty"`
	MatchCriteria        []string          `json:"MatchCriteria,omitempty"`
	ConnectionProperties map[string]string `json:"ConnectionProperties,omitempty"`
}

type connectionJSON struct {
	Name                 string            `json:"Name"`
	Description          string            `json:"Description,omitempty"`
	ConnectionType       string            `json:"ConnectionType,omitempty"`
	MatchCriteria        []string          `json:"MatchCriteria,omitempty"`
	ConnectionProperties map[string]string `json:"ConnectionProperties,omitempty"`
	CreationTime         *float64          `json:"CreationTime,omitempty"`
	LastUpdatedTime      *float64          `json:"LastUpdatedTime,omitempty"`
}

func connToWire(c *driver.Connection) connectionJSON {
	return connectionJSON{
		Name: c.Name, Description: c.Description, ConnectionType: c.ConnectionType,
		MatchCriteria: c.MatchCriteria, ConnectionProperties: c.ConnectionProperties,
		CreationTime: epochOrNil(c.CreationTime), LastUpdatedTime: epochOrNil(c.LastUpdatedTime),
	}
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func connFromInput(in connectionInputJSON) driver.Connection {
	return driver.Connection{
		Name: in.Name, Description: in.Description, ConnectionType: in.ConnectionType,
		MatchCriteria: in.MatchCriteria, ConnectionProperties: in.ConnectionProperties,
	}
}

type createConnectionRequest struct {
	CatalogID       string              `json:"CatalogId"`
	ConnectionInput connectionInputJSON `json:"ConnectionInput"`
}

func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createConnectionRequest) (any, error) {
		if err := h.glue.CreateConnection(ctx, req.CatalogID, connFromInput(req.ConnectionInput)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getConnectionRequest struct {
	CatalogID string `json:"CatalogId"`
	Name      string `json:"Name"`
}

type getConnectionResponse struct {
	Connection connectionJSON `json:"Connection"`
}

func (h *Handler) getConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getConnectionRequest) (any, error) {
		c, err := h.glue.GetConnection(ctx, req.CatalogID, req.Name)
		if err != nil {
			return nil, err
		}

		return getConnectionResponse{Connection: connToWire(c)}, nil
	})
}

type updateConnectionRequest struct {
	CatalogID       string              `json:"CatalogId"`
	Name            string              `json:"Name"`
	ConnectionInput connectionInputJSON `json:"ConnectionInput"`
}

func (h *Handler) updateConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateConnectionRequest) (any, error) {
		if err := h.glue.UpdateConnection(ctx, req.CatalogID, req.Name,
			connFromInput(req.ConnectionInput)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getConnectionRequest) (any, error) {
		if err := h.glue.DeleteConnection(ctx, req.CatalogID, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getConnectionsRequest struct {
	CatalogID  string `json:"CatalogId"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type getConnectionsResponse struct {
	ConnectionList []connectionJSON `json:"ConnectionList"`
	NextToken      string           `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getConnections(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getConnectionsRequest) (any, error) {
		conns, next, err := h.glue.GetConnections(ctx, req.CatalogID,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]connectionJSON, 0, len(conns))
		for i := range conns {
			out = append(out, connToWire(&conns[i]))
		}

		return getConnectionsResponse{ConnectionList: out, NextToken: next}, nil
	})
}

type batchDeleteConnectionRequest struct {
	CatalogID          string   `json:"CatalogId"`
	ConnectionNameList []string `json:"ConnectionNameList"`
}

type batchDeleteConnectionResponse struct {
	Succeeded []string                   `json:"Succeeded"`
	Errors    map[string]errorDetailJSON `json:"Errors,omitempty"`
}

func (h *Handler) batchDeleteConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchDeleteConnectionRequest) (any, error) {
		errs, err := h.glue.BatchDeleteConnection(ctx, req.CatalogID, req.ConnectionNameList)
		if err != nil {
			return nil, err
		}

		succeeded := make([]string, 0, len(req.ConnectionNameList))
		errMap := map[string]errorDetailJSON{}

		for _, n := range req.ConnectionNameList {
			if be, bad := errs[n]; bad {
				errMap[n] = errorDetailJSON{ErrorCode: be.ErrorCode, ErrorMessage: be.ErrorMessage}
			} else {
				succeeded = append(succeeded, n)
			}
		}

		return batchDeleteConnectionResponse{Succeeded: succeeded, Errors: errMap}, nil
	})
}

type testConnectionRequest struct {
	ConnectionName string `json:"ConnectionName"`
}

func (h *Handler) testConnection(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *testConnectionRequest) (any, error) {
		if err := h.glue.TestConnection(ctx, req.ConnectionName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// --- catalogs ---

type catalogInputJSON struct {
	Description string `json:"Description,omitempty"`
}

type catalogJSON struct {
	CatalogID   string   `json:"CatalogID,omitempty"`
	Name        string   `json:"Name,omitempty"`
	Description string   `json:"Description,omitempty"`
	CreateTime  *float64 `json:"CreateTime,omitempty"`
	UpdateTime  *float64 `json:"UpdateTime,omitempty"`
}

func catalogToWire(c *driver.Catalog) catalogJSON {
	return catalogJSON{
		CatalogID: c.CatalogID, Name: c.Name, Description: c.Description,
		CreateTime: epochOrNil(c.CreateTime), UpdateTime: epochOrNil(c.UpdateTime),
	}
}

type createCatalogRequest struct {
	Name         string           `json:"Name"`
	CatalogInput catalogInputJSON `json:"CatalogInput"`
}

func (h *Handler) createCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createCatalogRequest) (any, error) {
		if err := h.glue.CreateCatalog(ctx, driver.Catalog{
			Name: req.Name, Description: req.CatalogInput.Description,
		}); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type catalogIDRequest struct {
	CatalogID string `json:"CatalogId"`
}

type getCatalogResponse struct {
	Catalog catalogJSON `json:"Catalog"`
}

func (h *Handler) getCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *catalogIDRequest) (any, error) {
		c, err := h.glue.GetCatalog(ctx, req.CatalogID)
		if err != nil {
			return nil, err
		}

		return getCatalogResponse{Catalog: catalogToWire(c)}, nil
	})
}

type updateCatalogRequest struct {
	CatalogID    string           `json:"CatalogId"`
	CatalogInput catalogInputJSON `json:"CatalogInput"`
}

func (h *Handler) updateCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateCatalogRequest) (any, error) {
		if err := h.glue.UpdateCatalog(ctx, req.CatalogID, driver.Catalog{
			Description: req.CatalogInput.Description,
		}); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *catalogIDRequest) (any, error) {
		if err := h.glue.DeleteCatalog(ctx, req.CatalogID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getCatalogsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type getCatalogsResponse struct {
	CatalogList []catalogJSON `json:"CatalogList"`
	NextToken   string        `json:"NextToken,omitempty"`
}

func (h *Handler) getCatalogs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getCatalogsRequest) (any, error) {
		cats, next, err := h.glue.GetCatalogs(ctx,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]catalogJSON, 0, len(cats))
		for i := range cats {
			out = append(out, catalogToWire(&cats[i]))
		}

		return getCatalogsResponse{CatalogList: out, NextToken: next}, nil
	})
}
