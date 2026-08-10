package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

type putResourceConfigReq struct {
	ResourceType    string            `json:"ResourceType"`
	SchemaVersionID string            `json:"SchemaVersionId"`
	ResourceID      string            `json:"ResourceId"`
	ResourceName    string            `json:"ResourceName"`
	Configuration   string            `json:"Configuration"`
	Tags            map[string]string `json:"Tags"`
}

func (h *Handler) putResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putResourceConfigReq) (any, error) {
		item := cfgdriver.ConfigurationItem{
			ResourceType:  req.ResourceType,
			ResourceID:    req.ResourceID,
			ResourceName:  req.ResourceName,
			Configuration: req.Configuration,
			Tags:          req.Tags,
		}

		if err := h.cfg.PutResourceConfig(ctx, item); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type resourceHistoryReq struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	NextToken    string `json:"nextToken"`
	Limit        int32  `json:"limit"`
}

type resourceHistoryResp struct {
	ConfigurationItems []configurationItemJSON `json:"configurationItems"`
	NextToken          string                  `json:"nextToken,omitempty"`
}

func (h *Handler) getResourceConfigHistory(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceHistoryReq) (any, error) {
		items, next, err := h.cfg.GetResourceConfigHistory(
			ctx, req.ResourceType, req.ResourceID, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]configurationItemJSON, 0, len(items))
		for i := range items {
			out = append(out, itemToWire(&items[i]))
		}

		return resourceHistoryResp{ConfigurationItems: out, NextToken: next}, nil
	})
}

type deleteResourceConfigReq struct {
	ResourceType string `json:"ResourceType"`
	ResourceID   string `json:"ResourceId"`
}

func (h *Handler) deleteResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteResourceConfigReq) (any, error) {
		if err := h.cfg.DeleteResourceConfig(ctx, req.ResourceType, req.ResourceID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type batchGetResourceConfigReq struct {
	ResourceKeys []resourceKeyJSON `json:"resourceKeys"`
}

func (h *Handler) batchGetResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetResourceConfigReq) (any, error) {
		found, unproc, err := h.cfg.BatchGetResourceConfig(ctx, driverKeys(req.ResourceKeys))
		if err != nil {
			return nil, err
		}

		items := make([]configurationItemJSON, 0, len(found))
		for i := range found {
			items = append(items, itemToWire(&found[i]))
		}

		unprocessed := make([]resourceKeyJSON, 0, len(unproc))
		for _, k := range unproc {
			unprocessed = append(unprocessed, resourceKeyToWire(k))
		}

		return map[string]any{
			"baseConfigurationItems": items, "unprocessedResourceKeys": unprocessed,
		}, nil
	})
}

type listDiscoveredReq struct {
	ResourceType string   `json:"resourceType"`
	ResourceIDs  []string `json:"resourceIds"`
	NextToken    string   `json:"nextToken"`
	Limit        int32    `json:"limit"`
}

func (h *Handler) listDiscoveredResources(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listDiscoveredReq) (any, error) {
		keys, next, err := h.cfg.ListDiscoveredResources(
			ctx, req.ResourceType, req.ResourceIDs, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]resourceIdentifierJSON, 0, len(keys))
		for _, k := range keys {
			out = append(out, resourceIdentifierJSON{ResourceType: k.ResourceType, ResourceID: k.ResourceID})
		}

		return map[string]any{"resourceIdentifiers": out, "nextToken": next}, nil
	})
}

type discoveredCountsReq struct {
	ResourceTypes []string `json:"resourceTypes"`
	NextToken     string   `json:"nextToken"`
	Limit         int32    `json:"limit"`
}

func (h *Handler) getDiscoveredResourceCounts(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *discoveredCountsReq) (any, error) {
		total, counts, next, err := h.cfg.GetDiscoveredResourceCounts(
			ctx, req.ResourceTypes, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]resourceCountJSON, 0, len(counts))
		for i := range counts {
			out = append(out, resourceCountJSON{ResourceType: counts[i].ResourceType, Count: counts[i].Count})
		}

		return map[string]any{"totalDiscoveredResources": total, "resourceCounts": out, "nextToken": next}, nil
	})
}

type selectReq struct {
	Expression string `json:"Expression"`
	NextToken  string `json:"NextToken"`
	Limit      int32  `json:"Limit"`
}

func (h *Handler) selectResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *selectReq) (any, error) {
		rows, next, err := h.cfg.SelectResourceConfig(ctx, req.Expression, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{"Results": rows, "NextToken": next}, nil
	})
}

type startResourceEvalReq struct {
	EvaluationMode  string `json:"EvaluationMode"`
	ResourceDetails *struct {
		ResourceID            string `json:"ResourceId"`
		ResourceType          string `json:"ResourceType"`
		ResourceConfiguration string `json:"ResourceConfiguration"`
	} `json:"ResourceDetails"`
}

func (h *Handler) startResourceEvaluation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startResourceEvalReq) (any, error) {
		resourceType, config := "", ""
		if req.ResourceDetails != nil {
			resourceType = req.ResourceDetails.ResourceType
			config = req.ResourceDetails.ResourceConfiguration
		}

		id, err := h.cfg.StartResourceEvaluation(ctx, resourceType, config)
		if err != nil {
			return nil, err
		}

		return map[string]any{"ResourceEvaluationID": id}, nil
	})
}

type getResourceEvalSummaryReq struct {
	ResourceEvaluationID string `json:"ResourceEvaluationId"`
}

func (h *Handler) getResourceEvaluationSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getResourceEvalSummaryReq) (any, error) {
		status, resourceType, err := h.cfg.GetResourceEvaluationSummary(ctx, req.ResourceEvaluationID)
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"ResourceEvaluationID": req.ResourceEvaluationID,
			"EvaluationStatus":     map[string]any{"Status": status},
			"ResourceDetails":      map[string]any{"ResourceType": resourceType},
		}, nil
	})
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) listResourceEvaluations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		ids, next, err := h.cfg.ListResourceEvaluations(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{"ResourceEvaluationID": id})
		}

		return map[string]any{"ResourceEvaluations": out, "NextToken": next}, nil
	})
}
