package configservice

import (
	"context"
	"net/http"
)

type putRuleReq struct {
	ConfigRule *configRuleJSON `json:"ConfigRule"`
	Tags       []tag           `json:"Tags"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) putConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRuleReq) (any, error) {
		if req.ConfigRule == nil {
			return nil, invalidRequest("ConfigRule is required")
		}

		rule := req.ConfigRule.toDriver()
		rule.Tags = tagsToMap(req.Tags)

		if err := h.cfg.PutConfigRule(ctx, rule); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type describeRulesReq struct {
	ConfigRuleNames []string `json:"ConfigRuleNames"`
	NextToken       string   `json:"NextToken"`
	Limit           int32    `json:"Limit"`
}

type describeRulesResp struct {
	ConfigRules []configRuleJSON `json:"ConfigRules"`
	NextToken   string           `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConfigRules(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeRulesReq) (any, error) {
		rules, next, err := h.cfg.DescribeConfigRules(ctx, req.ConfigRuleNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]configRuleJSON, 0, len(rules))
		for i := range rules {
			out = append(out, ruleToWire(&rules[i]))
		}

		return describeRulesResp{ConfigRules: out, NextToken: next}, nil
	})
}

type ruleNameReq struct {
	ConfigRuleName string `json:"ConfigRuleName"`
}

func (h *Handler) deleteConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ruleNameReq) (any, error) {
		if err := h.cfg.DeleteConfigRule(ctx, req.ConfigRuleName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type ruleEvalStatusJSON struct {
	ConfigRuleName               string   `json:"ConfigRuleName,omitempty"`
	ConfigRuleArn                string   `json:"ConfigRuleArn,omitempty"`
	ConfigRuleID                 string   `json:"ConfigRuleId,omitempty"`
	LastSuccessfulEvaluationTime *float64 `json:"LastSuccessfulEvaluationTime,omitempty"`
}

type describeRuleEvalStatusResp struct {
	ConfigRulesEvaluationStatus []ruleEvalStatusJSON `json:"ConfigRulesEvaluationStatus"`
	NextToken                   string               `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConfigRuleEvaluationStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeRulesReq) (any, error) {
		rules, next, err := h.cfg.DescribeConfigRuleEvaluationStatus(
			ctx, req.ConfigRuleNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]ruleEvalStatusJSON, 0, len(rules))
		for i := range rules {
			out = append(out, ruleEvalStatusJSON{
				ConfigRuleName:               rules[i].ConfigRuleName,
				ConfigRuleArn:                rules[i].ConfigRuleArn,
				ConfigRuleID:                 rules[i].ConfigRuleID,
				LastSuccessfulEvaluationTime: epochOrNil(rules[i].LastSuccessfulEval),
			})
		}

		return describeRuleEvalStatusResp{ConfigRulesEvaluationStatus: out, NextToken: next}, nil
	})
}

type startEvalReq struct {
	ConfigRuleNames []string `json:"ConfigRuleNames"`
}

func (h *Handler) startConfigRulesEvaluation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startEvalReq) (any, error) {
		if err := h.cfg.StartConfigRulesEvaluation(ctx, req.ConfigRuleNames); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type putEvaluationsReq struct {
	Evaluations []evaluationJSON `json:"Evaluations"`
	ResultToken string           `json:"ResultToken"`
	TestMode    bool             `json:"TestMode"`
}

type putEvaluationsResp struct {
	FailedEvaluations []evaluationJSON `json:"FailedEvaluations,omitempty"`
}

func (h *Handler) putEvaluations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putEvaluationsReq) (any, error) {
		converted := toDriverEvals(req.Evaluations)

		failed, err := h.cfg.PutEvaluations(ctx, req.ResultToken, converted, req.TestMode)
		if err != nil {
			return nil, err
		}

		out := make([]evaluationJSON, 0, len(failed))
		for i := range failed {
			out = append(out, evalToWire(&failed[i]))
		}

		return putEvaluationsResp{FailedEvaluations: out}, nil
	})
}

type putExternalEvalReq struct {
	ConfigRuleName string         `json:"ConfigRuleName"`
	Evaluation     evaluationJSON `json:"Evaluation"`
}

func (h *Handler) putExternalEvaluation(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putExternalEvalReq) (any, error) {
		if err := h.cfg.PutExternalEvaluation(ctx, req.ConfigRuleName, req.Evaluation.toDriver()); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteEvaluationResults(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ruleNameReq) (any, error) {
		if err := h.cfg.DeleteEvaluationResults(ctx, req.ConfigRuleName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getCustomRulePolicyResp struct {
	PolicyText string `json:"PolicyText,omitempty"`
}

func (h *Handler) getCustomRulePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *ruleNameReq) (any, error) {
		policy, err := h.cfg.GetCustomRulePolicy(ctx, req.ConfigRuleName)
		if err != nil {
			return nil, err
		}

		return getCustomRulePolicyResp{PolicyText: policy}, nil
	})
}
