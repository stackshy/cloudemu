package kendra

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerIndexRoutes wires the index operations.
func (h *Handler) registerIndexRoutes() {
	h.routes["CreateIndex"] = h.createIndex
	h.routes["DescribeIndex"] = h.describeIndex
	h.routes["UpdateIndex"] = h.updateIndex
	h.routes["DeleteIndex"] = h.deleteIndex
	h.routes["ListIndices"] = h.listIndices
}

type createIndexRequest struct {
	ClientToken                       string          `json:"ClientToken"`
	Name                              string          `json:"Name"`
	Edition                           string          `json:"Edition"`
	RoleArn                           string          `json:"RoleArn"`
	Description                       string          `json:"Description"`
	UserContextPolicy                 string          `json:"UserContextPolicy"`
	ServerSideEncryptionConfiguration json.RawMessage `json:"ServerSideEncryptionConfiguration"`
	UserGroupResolutionConfiguration  json.RawMessage `json:"UserGroupResolutionConfiguration"`
	UserTokenConfigurations           json.RawMessage `json:"UserTokenConfigurations"`
	Tags                              []tagJSON       `json:"Tags"`
}

type createIndexResponse struct {
	ID string `json:"Id"`
}

func (h *Handler) createIndex(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createIndexRequest) (any, error) {
		idx, err := h.kendra.CreateIndex(ctx, &driver.CreateIndexInput{
			Name:                              req.Name,
			Edition:                           req.Edition,
			RoleArn:                           req.RoleArn,
			Description:                       req.Description,
			UserContextPolicy:                 req.UserContextPolicy,
			ServerSideEncryptionConfiguration: req.ServerSideEncryptionConfiguration,
			UserGroupResolutionConfiguration:  req.UserGroupResolutionConfiguration,
			UserTokenConfigurations:           req.UserTokenConfigurations,
			Tags:                              tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createIndexResponse{ID: idx.ID}, nil
	})
}

type describeIndexRequest struct {
	ID string `json:"Id"`
}

func (h *Handler) describeIndex(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeIndexRequest) (any, error) {
		idx, err := h.kendra.DescribeIndex(ctx, req.ID)
		if err != nil {
			return nil, err
		}

		return toIndexDescription(idx), nil
	})
}

type updateIndexRequest struct {
	ID                                   string          `json:"Id"`
	Name                                 *string         `json:"Name"`
	RoleArn                              *string         `json:"RoleArn"`
	Description                          *string         `json:"Description"`
	UserContextPolicy                    *string         `json:"UserContextPolicy"`
	CapacityUnits                        json.RawMessage `json:"CapacityUnits"`
	DocumentMetadataConfigurationUpdates json.RawMessage `json:"DocumentMetadataConfigurationUpdates"`
	UserGroupResolutionConfiguration     json.RawMessage `json:"UserGroupResolutionConfiguration"`
	UserTokenConfigurations              json.RawMessage `json:"UserTokenConfigurations"`
}

func (h *Handler) updateIndex(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateIndexRequest) (any, error) {
		err := h.kendra.UpdateIndex(ctx, &driver.UpdateIndexInput{
			ID:                                   req.ID,
			Name:                                 req.Name,
			RoleArn:                              req.RoleArn,
			Description:                          req.Description,
			UserContextPolicy:                    req.UserContextPolicy,
			CapacityUnits:                        req.CapacityUnits,
			DocumentMetadataConfigurationUpdates: req.DocumentMetadataConfigurationUpdates,
			UserGroupResolutionConfiguration:     req.UserGroupResolutionConfiguration,
			UserTokenConfigurations:              req.UserTokenConfigurations,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteIndexRequest struct {
	ID string `json:"Id"`
}

func (h *Handler) deleteIndex(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteIndexRequest) (any, error) {
		if err := h.kendra.DeleteIndex(ctx, req.ID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listIndicesRequest struct {
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listIndicesResponse struct {
	IndexConfigurationSummaryItems []indexSummaryJSON `json:"IndexConfigurationSummaryItems"`
	NextToken                      string             `json:"NextToken,omitempty"`
}

func (h *Handler) listIndices(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listIndicesRequest) (any, error) {
		indexes, next, err := h.kendra.ListIndices(ctx, driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		resp := listIndicesResponse{
			IndexConfigurationSummaryItems: make([]indexSummaryJSON, 0, len(indexes)),
			NextToken:                      next,
		}
		for i := range indexes {
			resp.IndexConfigurationSummaryItems = append(resp.IndexConfigurationSummaryItems, toIndexSummary(&indexes[i]))
		}

		return resp, nil
	})
}
