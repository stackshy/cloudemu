package configservice

import (
	"context"
	"net/http"
)

type aggQueryReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	NextToken                   string `json:"NextToken"`
	Limit                       int32  `json:"Limit"`
}

func (h *Handler) describeAggregateComplianceByConfigRules(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggQueryReq) (any, error) {
		rules, next, err := h.cfg.DescribeAggregateComplianceByConfigRules(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		type aggRuleJSON struct {
			ConfigRuleName string          `json:"ConfigRuleName,omitempty"`
			Compliance     *complianceJSON `json:"Compliance,omitempty"`
			AccountID      string          `json:"AccountId,omitempty"`
			AwsRegion      string          `json:"AwsRegion,omitempty"`
		}

		out := make([]aggRuleJSON, 0, len(rules))
		for i := range rules {
			out = append(out, aggRuleJSON{
				ConfigRuleName: rules[i].ConfigRuleName,
				Compliance:     &complianceJSON{ComplianceType: rules[i].Compliance},
				AccountID:      h.accountID,
				AwsRegion:      h.region,
			})
		}

		return map[string]any{"AggregateComplianceByConfigRules": out, "NextToken": next}, nil
	})
}

func (h *Handler) describeAggregateComplianceByConformancePacks(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggQueryReq) (any, error) {
		packs, next, err := h.cfg.DescribeAggregateComplianceByConformancePacks(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		type aggPackJSON struct {
			ConformancePackName string `json:"ConformancePackName,omitempty"`
			AccountID           string `json:"AccountId,omitempty"`
			AwsRegion           string `json:"AwsRegion,omitempty"`
		}

		out := make([]aggPackJSON, 0, len(packs))
		for i := range packs {
			out = append(out, aggPackJSON{
				ConformancePackName: packs[i].ConformancePackName, AccountID: h.accountID, AwsRegion: h.region,
			})
		}

		return map[string]any{"AggregateComplianceByConformancePacks": out, "NextToken": next}, nil
	})
}

type aggDetailsReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	ConfigRuleName              string `json:"ConfigRuleName"`
	AccountID                   string `json:"AccountId"`
	AwsRegion                   string `json:"AwsRegion"`
	NextToken                   string `json:"NextToken"`
	Limit                       int32  `json:"Limit"`
}

func (h *Handler) getAggregateComplianceDetailsByConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggDetailsReq) (any, error) {
		evals, next, err := h.cfg.GetAggregateComplianceDetailsByConfigRule(
			ctx, req.ConfigurationAggregatorName, req.ConfigRuleName, req.AccountID, req.AwsRegion,
			pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{"AggregateEvaluationResults": evalResults(evals), "NextToken": next}, nil
	})
}

func (h *Handler) getAggregateConfigRuleComplianceSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggQueryReq) (any, error) {
		c, nc, err := h.cfg.GetAggregateConfigRuleComplianceSummary(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"GroupByKey": "ACCOUNT_ID",
			"AggregateComplianceCounts": []map[string]any{
				{"GroupName": h.accountID, "ComplianceSummary": summary(c, nc)},
			},
		}, nil
	})
}

func (h *Handler) getAggregateConformancePackComplianceSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggQueryReq) (any, error) {
		c, nc, err := h.cfg.GetAggregateConformancePackComplianceSummary(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"GroupByKey": "ACCOUNT_ID",
			"AggregateConformancePackComplianceSummaries": []map[string]any{
				{"GroupName": h.accountID, "ComplianceSummary": map[string]any{
					"CompliantConformancePackCount": c, "NonCompliantConformancePackCount": nc,
				}},
			},
		}, nil
	})
}

func (h *Handler) getAggregateDiscoveredResourceCounts(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggQueryReq) (any, error) {
		total, counts, next, err := h.cfg.GetAggregateDiscoveredResourceCounts(
			ctx, req.ConfigurationAggregatorName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{"TotalDiscoveredResources": total, "GroupedResourceCounts": counts, "NextToken": next},
			nil
	})
}

type aggResourceIdentifierReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	ResourceIdentifier          *struct {
		ResourceType    string `json:"ResourceType"`
		ResourceID      string `json:"ResourceId"`
		SourceAccountID string `json:"SourceAccountId"`
		SourceRegion    string `json:"SourceRegion"`
	} `json:"ResourceIdentifier"`
}

func (h *Handler) getAggregateResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *aggResourceIdentifierReq) (any, error) {
		if req.ResourceIdentifier == nil {
			return nil, invalidRequest("ResourceIdentifier is required")
		}

		item, err := h.cfg.GetAggregateResourceConfig(
			ctx, req.ConfigurationAggregatorName,
			req.ResourceIdentifier.ResourceType, req.ResourceIdentifier.ResourceID)
		if err != nil {
			return nil, err
		}

		return map[string]any{"ConfigurationItem": itemToWire(item)}, nil
	})
}

type batchGetAggReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	ResourceIdentifiers         []struct {
		ResourceType    string `json:"ResourceType"`
		ResourceID      string `json:"ResourceId"`
		SourceAccountID string `json:"SourceAccountId"`
		SourceRegion    string `json:"SourceRegion"`
	} `json:"ResourceIdentifiers"`
}

func (h *Handler) batchGetAggregateResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetAggReq) (any, error) {
		keys := make([]cfgResourceKey, 0, len(req.ResourceIdentifiers))
		for _, id := range req.ResourceIdentifiers {
			keys = append(keys, cfgResourceKey{ResourceType: id.ResourceType, ResourceID: id.ResourceID})
		}

		found, unproc, err := h.cfg.BatchGetAggregateResourceConfig(ctx, req.ConfigurationAggregatorName, keys)
		if err != nil {
			return nil, err
		}

		items := make([]configurationItemJSON, 0, len(found))
		for i := range found {
			items = append(items, itemToWire(&found[i]))
		}

		unprocessed := make([]map[string]any, 0, len(unproc))
		for _, k := range unproc {
			unprocessed = append(unprocessed,
				map[string]any{"ResourceType": k.ResourceType, "ResourceID": k.ResourceID})
		}

		return map[string]any{
			"BaseConfigurationItems": items, "UnprocessedResourceIdentifiers": unprocessed,
		}, nil
	})
}

type listAggResourcesReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	ResourceType                string `json:"ResourceType"`
	NextToken                   string `json:"NextToken"`
	Limit                       int32  `json:"Limit"`
}

func (h *Handler) listAggregateDiscoveredResources(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listAggResourcesReq) (any, error) {
		keys, next, err := h.cfg.ListAggregateDiscoveredResources(
			ctx, req.ConfigurationAggregatorName, req.ResourceType, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, map[string]any{
				"ResourceType": k.ResourceType, "ResourceID": k.ResourceID,
				"SourceAccountID": h.accountID, "SourceRegion": h.region,
			})
		}

		return map[string]any{"ResourceIdentifiers": out, "NextToken": next}, nil
	})
}

type selectAggReq struct {
	ConfigurationAggregatorName string `json:"ConfigurationAggregatorName"`
	Expression                  string `json:"Expression"`
	NextToken                   string `json:"NextToken"`
	Limit                       int32  `json:"Limit"`
}

func (h *Handler) selectAggregateResourceConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *selectAggReq) (any, error) {
		rows, next, err := h.cfg.SelectAggregateResourceConfig(
			ctx, req.ConfigurationAggregatorName, req.Expression, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{"Results": rows, "NextToken": next}, nil
	})
}
