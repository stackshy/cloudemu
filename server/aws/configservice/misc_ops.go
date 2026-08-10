package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// --- Stored queries ---

type putStoredQueryReq struct {
	StoredQuery *storedQueryJSON `json:"StoredQuery"`
	Tags        []tag            `json:"Tags"`
}

type putStoredQueryResp struct {
	QueryArn string `json:"QueryArn,omitempty"`
}

func (h *Handler) putStoredQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putStoredQueryReq) (any, error) {
		if req.StoredQuery == nil {
			return nil, invalidRequest("StoredQuery is required")
		}

		q := cfgdriver.StoredQuery{
			QueryName:   req.StoredQuery.QueryName,
			Description: req.StoredQuery.Description,
			Expression:  req.StoredQuery.Expression,
		}

		arn, err := h.cfg.PutStoredQuery(ctx, q, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return putStoredQueryResp{QueryArn: arn}, nil
	})
}

type queryNameReq struct {
	QueryName string `json:"QueryName"`
}

type getStoredQueryResp struct {
	StoredQuery storedQueryJSON `json:"StoredQuery"`
}

func (h *Handler) getStoredQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *queryNameReq) (any, error) {
		q, err := h.cfg.GetStoredQuery(ctx, req.QueryName)
		if err != nil {
			return nil, err
		}

		return getStoredQueryResp{StoredQuery: storedQueryJSON{
			QueryArn: q.QueryArn, QueryID: q.QueryID, QueryName: q.QueryName,
			Description: q.Description, Expression: q.Expression,
		}}, nil
	})
}

type listStoredQueriesResp struct {
	StoredQueryMetadata []storedQueryMetaJSON `json:"StoredQueryMetadata"`
	NextToken           string                `json:"NextToken,omitempty"`
}

func (h *Handler) listStoredQueries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		qs, next, err := h.cfg.ListStoredQueries(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]storedQueryMetaJSON, 0, len(qs))
		for i := range qs {
			out = append(out, storedQueryMetaJSON{
				QueryArn: qs[i].QueryArn, QueryID: qs[i].QueryID,
				QueryName: qs[i].QueryName, Description: qs[i].Description,
			})
		}

		return listStoredQueriesResp{StoredQueryMetadata: out, NextToken: next}, nil
	})
}

type deleteStoredQueryReq struct {
	QueryName string `json:"QueryName"`
}

func (h *Handler) deleteStoredQuery(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteStoredQueryReq) (any, error) {
		if err := h.cfg.DeleteStoredQuery(ctx, req.QueryName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// --- Retention ---

type putRetentionReq struct {
	RetentionPeriodInDays int32 `json:"RetentionPeriodInDays"`
}

type putRetentionResp struct {
	RetentionConfiguration retentionJSON `json:"RetentionConfiguration"`
}

func (h *Handler) putRetentionConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRetentionReq) (any, error) {
		rc, err := h.cfg.PutRetentionConfiguration(ctx, req.RetentionPeriodInDays)
		if err != nil {
			return nil, err
		}

		return putRetentionResp{RetentionConfiguration: retentionJSON{
			Name: rc.Name, RetentionPeriodInDays: rc.RetentionPeriodInDays,
		}}, nil
	})
}

type retentionNamesReq struct {
	RetentionConfigurationNames []string `json:"RetentionConfigurationNames"`
	NextToken                   string   `json:"NextToken"`
	Limit                       int32    `json:"Limit"`
}

type describeRetentionResp struct {
	RetentionConfigurations []retentionJSON `json:"RetentionConfigurations"`
	NextToken               string          `json:"NextToken,omitempty"`
}

func (h *Handler) describeRetentionConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *retentionNamesReq) (any, error) {
		rcs, next, err := h.cfg.DescribeRetentionConfigurations(
			ctx, req.RetentionConfigurationNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]retentionJSON, 0, len(rcs))
		for i := range rcs {
			out = append(out, retentionJSON{Name: rcs[i].Name, RetentionPeriodInDays: rcs[i].RetentionPeriodInDays})
		}

		return describeRetentionResp{RetentionConfigurations: out, NextToken: next}, nil
	})
}

type retentionNameReq struct {
	RetentionConfigurationName string `json:"RetentionConfigurationName"`
}

func (h *Handler) deleteRetentionConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *retentionNameReq) (any, error) {
		if err := h.cfg.DeleteRetentionConfiguration(ctx, req.RetentionConfigurationName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// --- Connectors ---

type putConnectorReq struct {
	Name              string `json:"Name"`
	ConnectorAgentArn string `json:"ConnectorAgentArn"`
}

type putConnectorResp struct {
	ConnectorArn string `json:"ConnectorArn,omitempty"`
}

func (h *Handler) putConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putConnectorReq) (any, error) {
		arn, err := h.cfg.PutConnector(ctx, req.Name, req.ConnectorAgentArn)
		if err != nil {
			return nil, err
		}

		return putConnectorResp{ConnectorArn: arn}, nil
	})
}

type connectorNameReq struct {
	Name string `json:"Name"`
}

func (h *Handler) getConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *connectorNameReq) (any, error) {
		arn, agentArn, err := h.cfg.GetConnector(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return map[string]any{"ConnectorArn": arn, "ConnectorAgentArn": agentArn, "Name": req.Name}, nil
	})
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) listConnectors(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		names, next, err := h.cfg.ListConnectors(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]map[string]any, 0, len(names))
		for _, n := range names {
			out = append(out, map[string]any{"Name": n})
		}

		return map[string]any{"Connectors": out, "NextToken": next}, nil
	})
}

func (h *Handler) deleteConnector(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *connectorNameReq) (any, error) {
		if err := h.cfg.DeleteConnector(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

// --- Tags ---

type tagResourceReq struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []tag  `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceReq) (any, error) {
		if err := h.cfg.TagResource(ctx, req.ResourceArn, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceReq struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceReq) (any, error) {
		if err := h.cfg.UntagResource(ctx, req.ResourceArn, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsReq struct {
	ResourceArn string `json:"ResourceArn"`
	NextToken   string `json:"NextToken"`
	Limit       int32  `json:"Limit"`
}

type listTagsResp struct {
	Tags      []tag  `json:"Tags"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsReq) (any, error) {
		tags, next, err := h.cfg.ListTagsForResource(ctx, req.ResourceArn, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return listTagsResp{Tags: mapToTags(tags), NextToken: next}, nil
	})
}
