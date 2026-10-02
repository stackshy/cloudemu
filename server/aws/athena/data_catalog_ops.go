package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type createDataCatalogRequest struct {
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	Description string            `json:"Description"`
	Parameters  map[string]string `json:"Parameters"`
	Tags        []tagJSON         `json:"Tags"`
}

type dataCatalogResponse struct {
	DataCatalog dataCatalogJSON `json:"DataCatalog"`
}

func (h *Handler) createDataCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDataCatalogRequest) (any, error) {
		dc, err := h.athena.CreateDataCatalog(ctx, driver.CreateDataCatalogInput{
			Name:        req.Name,
			Type:        req.Type,
			Description: req.Description,
			Parameters:  req.Parameters,
			Tags:        tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return dataCatalogResponse{DataCatalog: dataCatalogToWire(dc)}, nil
	})
}

type getDataCatalogRequest struct {
	Name      string `json:"Name"`
	WorkGroup string `json:"WorkGroup"`
}

func (h *Handler) getDataCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getDataCatalogRequest) (any, error) {
		dc, err := h.athena.GetDataCatalog(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return dataCatalogResponse{DataCatalog: dataCatalogToWire(dc)}, nil
	})
}

type listDataCatalogsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
	WorkGroup  string `json:"WorkGroup"`
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
			out = append(out, dataCatalogSummaryJSON{
				CatalogName:    c.CatalogName,
				Type:           c.Type,
				Status:         c.Status,
				ConnectionType: c.ConnectionType,
				Error:          c.Error,
			})
		}

		return listDataCatalogsResponse{DataCatalogsSummary: out, NextToken: next}, nil
	})
}

type updateDataCatalogRequest struct {
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	Description *string           `json:"Description"`
	Parameters  map[string]string `json:"Parameters"`
}

func (h *Handler) updateDataCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateDataCatalogRequest) (any, error) {
		err := h.athena.UpdateDataCatalog(ctx, driver.UpdateDataCatalogInput{
			Name:        req.Name,
			Type:        req.Type,
			Description: req.Description,
			Parameters:  req.Parameters,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteDataCatalogRequest struct {
	Name              string `json:"Name"`
	DeleteCatalogOnly bool   `json:"DeleteCatalogOnly"`
}

func (h *Handler) deleteDataCatalog(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteDataCatalogRequest) (any, error) {
		dc, err := h.athena.DeleteDataCatalog(ctx, req.Name, req.DeleteCatalogOnly)
		if err != nil {
			return nil, err
		}

		return dataCatalogResponse{DataCatalog: dataCatalogToWire(dc)}, nil
	})
}
