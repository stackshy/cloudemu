package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

type cfgEvaluation = cfgdriver.Evaluation

type describeComplianceByRuleResp struct {
	ComplianceByConfigRules []complianceByRuleJSON `json:"ComplianceByConfigRules"`
	NextToken               string                 `json:"NextToken,omitempty"`
}

func (h *Handler) describeComplianceByConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeRulesReq) (any, error) {
		rules, next, err := h.cfg.DescribeComplianceByConfigRule(
			ctx, req.ConfigRuleNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]complianceByRuleJSON, 0, len(rules))
		for i := range rules {
			out = append(out, complianceByRuleJSON{
				ConfigRuleName: rules[i].ConfigRuleName,
				Compliance:     &complianceJSON{ComplianceType: rules[i].Compliance},
			})
		}

		return describeComplianceByRuleResp{ComplianceByConfigRules: out, NextToken: next}, nil
	})
}

type resourceComplianceReq struct {
	ResourceType string `json:"ResourceType"`
	ResourceID   string `json:"ResourceId"`
	NextToken    string `json:"NextToken"`
	Limit        int32  `json:"Limit"`
}

type complianceByResourceJSON struct {
	ResourceType string          `json:"ResourceType,omitempty"`
	ResourceID   string          `json:"ResourceId,omitempty"`
	Compliance   *complianceJSON `json:"Compliance,omitempty"`
}

type describeComplianceByResourceResp struct {
	ComplianceByResources []complianceByResourceJSON `json:"ComplianceByResources"`
	NextToken             string                     `json:"NextToken,omitempty"`
}

func (h *Handler) describeComplianceByResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceComplianceReq) (any, error) {
		items, next, err := h.cfg.DescribeComplianceByResource(
			ctx, req.ResourceType, req.ResourceID, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]complianceByResourceJSON, 0, len(items))
		for i := range items {
			out = append(out, complianceByResourceJSON{
				ResourceType: items[i].ResourceType,
				ResourceID:   items[i].ResourceID,
				Compliance:   &complianceJSON{ComplianceType: "COMPLIANT"},
			})
		}

		return describeComplianceByResourceResp{ComplianceByResources: out, NextToken: next}, nil
	})
}

type evaluationResultJSON struct {
	ComplianceType     string   `json:"ComplianceType,omitempty"`
	Annotation         string   `json:"Annotation,omitempty"`
	ResultRecordedTime *float64 `json:"ResultRecordedTime,omitempty"`
}

type complianceDetailsResp struct {
	EvaluationResults []evaluationResultJSON `json:"EvaluationResults"`
	NextToken         string                 `json:"NextToken,omitempty"`
}

func (h *Handler) getComplianceDetailsByConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ruleNamePageReq) (any, error) {
		evals, next, err := h.cfg.GetComplianceDetailsByConfigRule(
			ctx, req.ConfigRuleName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return complianceDetailsResp{EvaluationResults: evalResults(evals), NextToken: next}, nil
	})
}

func (h *Handler) getComplianceDetailsByResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resourceComplianceReq) (any, error) {
		evals, next, err := h.cfg.GetComplianceDetailsByResource(
			ctx, req.ResourceType, req.ResourceID, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return complianceDetailsResp{EvaluationResults: evalResults(evals), NextToken: next}, nil
	})
}

type complianceSummaryJSON struct {
	CompliantResourceCount    *countJSON `json:"CompliantResourceCount,omitempty"`
	NonCompliantResourceCount *countJSON `json:"NonCompliantResourceCount,omitempty"`
}

type countJSON struct {
	CappedCount int32 `json:"CappedCount"`
}

type complianceSummaryResp struct {
	ComplianceSummary *complianceSummaryJSON `json:"ComplianceSummary,omitempty"`
}

func (h *Handler) getComplianceSummaryByConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, _ *struct{}) (any, error) {
		c, nc, err := h.cfg.GetComplianceSummaryByConfigRule(ctx)
		if err != nil {
			return nil, err
		}

		return complianceSummaryResp{ComplianceSummary: summary(c, nc)}, nil
	})
}

type summaryByTypeReq struct {
	ResourceTypes []string `json:"ResourceTypes"`
}

type summaryByTypeResp struct {
	ComplianceSummariesByResourceType []struct {
		ResourceType      string                 `json:"ResourceType,omitempty"`
		ComplianceSummary *complianceSummaryJSON `json:"ComplianceSummary,omitempty"`
	} `json:"ComplianceSummariesByResourceType"`
}

func (h *Handler) getComplianceSummaryByResourceType(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *summaryByTypeReq) (any, error) {
		c, nc, err := h.cfg.GetComplianceSummaryByResourceType(ctx, req.ResourceTypes)
		if err != nil {
			return nil, err
		}

		var resp summaryByTypeResp
		resp.ComplianceSummariesByResourceType = append(resp.ComplianceSummariesByResourceType, struct {
			ResourceType      string                 `json:"ResourceType,omitempty"`
			ComplianceSummary *complianceSummaryJSON `json:"ComplianceSummary,omitempty"`
		}{ResourceType: "", ComplianceSummary: summary(c, nc)})

		return resp, nil
	})
}

// evalResults converts driver evaluations into wire EvaluationResult records.
func evalResults(evals []cfgEvaluation) []evaluationResultJSON {
	out := make([]evaluationResultJSON, 0, len(evals))
	for i := range evals {
		out = append(out, evaluationResultJSON{
			ComplianceType:     evals[i].ComplianceType,
			Annotation:         evals[i].Annotation,
			ResultRecordedTime: epochOrNil(evals[i].OrderingTimestamp),
		})
	}

	return out
}

func summary(compliant, nonCompliant int32) *complianceSummaryJSON {
	return &complianceSummaryJSON{
		CompliantResourceCount:    &countJSON{CappedCount: compliant},
		NonCompliantResourceCount: &countJSON{CappedCount: nonCompliant},
	}
}
