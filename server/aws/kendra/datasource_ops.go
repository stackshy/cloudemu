package kendra

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerDataSourceRoutes wires the data source operations.
func (h *Handler) registerDataSourceRoutes() {
	h.routes["CreateDataSource"] = h.createDataSource
	h.routes["DescribeDataSource"] = h.describeDataSource
	h.routes["UpdateDataSource"] = h.updateDataSource
	h.routes["DeleteDataSource"] = h.deleteDataSource
	h.routes["ListDataSources"] = h.listDataSources
}

type createDataSourceRequest struct {
	ClientToken                           string          `json:"ClientToken"`
	IndexID                               string          `json:"IndexId"`
	Name                                  string          `json:"Name"`
	Type                                  string          `json:"Type"`
	RoleArn                               string          `json:"RoleArn"`
	Description                           string          `json:"Description"`
	Schedule                              string          `json:"Schedule"`
	LanguageCode                          string          `json:"LanguageCode"`
	Configuration                         json.RawMessage `json:"Configuration"`
	VpcConfiguration                      json.RawMessage `json:"VpcConfiguration"`
	CustomDocumentEnrichmentConfiguration json.RawMessage `json:"CustomDocumentEnrichmentConfiguration"`
	Tags                                  []tagJSON       `json:"Tags"`
}

type createDataSourceResponse struct {
	ID string `json:"Id"`
}

func (h *Handler) createDataSource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDataSourceRequest) (any, error) {
		ds, err := h.kendra.CreateDataSource(ctx, &driver.CreateDataSourceInput{
			IndexID:                               req.IndexID,
			Name:                                  req.Name,
			Type:                                  req.Type,
			RoleArn:                               req.RoleArn,
			Description:                           req.Description,
			Schedule:                              req.Schedule,
			LanguageCode:                          req.LanguageCode,
			Configuration:                         req.Configuration,
			VpcConfiguration:                      req.VpcConfiguration,
			CustomDocumentEnrichmentConfiguration: req.CustomDocumentEnrichmentConfiguration,
			Tags:                                  tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createDataSourceResponse{ID: ds.ID}, nil
	})
}

type describeDataSourceRequest struct {
	ID      string `json:"Id"`
	IndexID string `json:"IndexId"`
}

func (h *Handler) describeDataSource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeDataSourceRequest) (any, error) {
		ds, err := h.kendra.DescribeDataSource(ctx, req.IndexID, req.ID)
		if err != nil {
			return nil, err
		}

		return toDataSourceDescription(ds), nil
	})
}

type updateDataSourceRequest struct {
	ID                                    string          `json:"Id"`
	IndexID                               string          `json:"IndexId"`
	Name                                  *string         `json:"Name"`
	RoleArn                               *string         `json:"RoleArn"`
	Description                           *string         `json:"Description"`
	Schedule                              *string         `json:"Schedule"`
	LanguageCode                          *string         `json:"LanguageCode"`
	Configuration                         json.RawMessage `json:"Configuration"`
	VpcConfiguration                      json.RawMessage `json:"VpcConfiguration"`
	CustomDocumentEnrichmentConfiguration json.RawMessage `json:"CustomDocumentEnrichmentConfiguration"`
}

func (h *Handler) updateDataSource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateDataSourceRequest) (any, error) {
		err := h.kendra.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
			ID:                                    req.ID,
			IndexID:                               req.IndexID,
			Name:                                  req.Name,
			RoleArn:                               req.RoleArn,
			Description:                           req.Description,
			Schedule:                              req.Schedule,
			LanguageCode:                          req.LanguageCode,
			Configuration:                         req.Configuration,
			VpcConfiguration:                      req.VpcConfiguration,
			CustomDocumentEnrichmentConfiguration: req.CustomDocumentEnrichmentConfiguration,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteDataSourceRequest struct {
	ID      string `json:"Id"`
	IndexID string `json:"IndexId"`
}

func (h *Handler) deleteDataSource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteDataSourceRequest) (any, error) {
		if err := h.kendra.DeleteDataSource(ctx, req.IndexID, req.ID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listDataSourcesRequest struct {
	IndexID    string `json:"IndexId"`
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listDataSourcesResponse struct {
	SummaryItems []dataSourceSummaryJSON `json:"SummaryItems"`
	NextToken    string                  `json:"NextToken,omitempty"`
}

func (h *Handler) listDataSources(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listDataSourcesRequest) (any, error) {
		dataSources, next, err := h.kendra.ListDataSources(ctx, req.IndexID,
			driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		resp := listDataSourcesResponse{
			SummaryItems: make([]dataSourceSummaryJSON, 0, len(dataSources)),
			NextToken:    next,
		}
		for i := range dataSources {
			resp.SummaryItems = append(resp.SummaryItems, toDataSourceSummary(&dataSources[i]))
		}

		return resp, nil
	})
}
