package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

type putOrgRuleReq struct {
	OrganizationConfigRuleName      string   `json:"OrganizationConfigRuleName"`
	ExcludedAccounts                []string `json:"ExcludedAccounts"`
	OrganizationManagedRuleMetadata *struct {
		RuleIdentifier            string `json:"RuleIdentifier"`
		Description               string `json:"Description"`
		InputParameters           string `json:"InputParameters"`
		MaximumExecutionFrequency string `json:"MaximumExecutionFrequency"`
	} `json:"OrganizationManagedRuleMetadata"`
}

type putOrgRuleResp struct {
	OrganizationConfigRuleArn string `json:"OrganizationConfigRuleArn,omitempty"`
}

func (h *Handler) putOrganizationConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putOrgRuleReq) (any, error) {
		rule := cfgdriver.OrganizationConfigRule{
			Name:             req.OrganizationConfigRuleName,
			ExcludedAccounts: req.ExcludedAccounts,
		}

		if req.OrganizationManagedRuleMetadata != nil {
			rule.ManagedRuleIdentifier = req.OrganizationManagedRuleMetadata.RuleIdentifier
			rule.Description = req.OrganizationManagedRuleMetadata.Description
			rule.InputParameters = req.OrganizationManagedRuleMetadata.InputParameters
			rule.MaximumExecutionFreq = req.OrganizationManagedRuleMetadata.MaximumExecutionFrequency
		}

		arn, err := h.cfg.PutOrganizationConfigRule(ctx, rule)
		if err != nil {
			return nil, err
		}

		return putOrgRuleResp{OrganizationConfigRuleArn: arn}, nil
	})
}

type orgRuleNamesReq struct {
	OrganizationConfigRuleNames []string `json:"OrganizationConfigRuleNames"`
	NextToken                   string   `json:"NextToken"`
	Limit                       int32    `json:"Limit"`
}

type orgRuleJSON struct {
	OrganizationConfigRuleName string   `json:"OrganizationConfigRuleName,omitempty"`
	OrganizationConfigRuleArn  string   `json:"OrganizationConfigRuleArn,omitempty"`
	ExcludedAccounts           []string `json:"ExcludedAccounts,omitempty"`
	LastUpdateTime             *float64 `json:"LastUpdateTime,omitempty"`
}

type describeOrgRulesResp struct {
	OrganizationConfigRules []orgRuleJSON `json:"OrganizationConfigRules"`
	NextToken               string        `json:"NextToken,omitempty"`
}

func orgRuleToWire(r *cfgdriver.OrganizationConfigRule) orgRuleJSON {
	return orgRuleJSON{
		OrganizationConfigRuleName: r.Name,
		OrganizationConfigRuleArn:  r.Arn,
		ExcludedAccounts:           r.ExcludedAccounts,
		LastUpdateTime:             epochOrNil(r.LastUpdateTime),
	}
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeOrganizationConfigRules(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgRuleNamesReq) (any, error) {
		rules, next, err := h.cfg.DescribeOrganizationConfigRules(
			ctx, req.OrganizationConfigRuleNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]orgRuleJSON, 0, len(rules))
		for i := range rules {
			out = append(out, orgRuleToWire(&rules[i]))
		}

		return describeOrgRulesResp{OrganizationConfigRules: out, NextToken: next}, nil
	})
}

type orgRuleStatusJSON struct {
	OrganizationConfigRuleName string `json:"OrganizationConfigRuleName,omitempty"`
	OrganizationRuleStatus     string `json:"OrganizationRuleStatus,omitempty"`
}

type describeOrgRuleStatusesResp struct {
	OrganizationConfigRuleStatuses []orgRuleStatusJSON `json:"OrganizationConfigRuleStatuses"`
	NextToken                      string              `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeOrganizationConfigRuleStatuses(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgRuleNamesReq) (any, error) {
		rules, next, err := h.cfg.DescribeOrganizationConfigRuleStatuses(
			ctx, req.OrganizationConfigRuleNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]orgRuleStatusJSON, 0, len(rules))
		for i := range rules {
			out = append(out, orgRuleStatusJSON{
				OrganizationConfigRuleName: rules[i].Name, OrganizationRuleStatus: "CREATE_SUCCESSFUL",
			})
		}

		return describeOrgRuleStatusesResp{OrganizationConfigRuleStatuses: out, NextToken: next}, nil
	})
}

type orgRuleNameReq struct {
	OrganizationConfigRuleName string `json:"OrganizationConfigRuleName"`
	NextToken                  string `json:"NextToken"`
	Limit                      int32  `json:"Limit"`
}

func (h *Handler) deleteOrganizationConfigRule(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgRuleNameReq) (any, error) {
		if err := h.cfg.DeleteOrganizationConfigRule(ctx, req.OrganizationConfigRuleName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type orgRuleDetailedStatusResp struct {
	OrganizationConfigRuleDetailedStatus []struct {
		AccountID      string `json:"AccountId,omitempty"`
		ConfigRuleName string `json:"ConfigRuleName,omitempty"`
	} `json:"OrganizationConfigRuleDetailedStatus,omitempty"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) getOrganizationConfigRuleDetailedStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgRuleNameReq) (any, error) {
		_, next, err := h.cfg.GetOrganizationConfigRuleDetailedStatus(
			ctx, req.OrganizationConfigRuleName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return orgRuleDetailedStatusResp{NextToken: next}, nil
	})
}

type getOrgPolicyResp struct {
	PolicyText string `json:"PolicyText,omitempty"`
}

func (h *Handler) getOrganizationCustomRulePolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgRuleNameReq) (any, error) {
		policy, err := h.cfg.GetOrganizationCustomRulePolicy(ctx, req.OrganizationConfigRuleName)
		if err != nil {
			return nil, err
		}

		return getOrgPolicyResp{PolicyText: policy}, nil
	})
}

type putOrgPackReq struct {
	OrganizationConformancePackName string               `json:"OrganizationConformancePackName"`
	TemplateBody                    string               `json:"TemplateBody"`
	TemplateS3Uri                   string               `json:"TemplateS3Uri"`
	DeliveryS3Bucket                string               `json:"DeliveryS3Bucket"`
	DeliveryS3KeyPrefix             string               `json:"DeliveryS3KeyPrefix"`
	ExcludedAccounts                []string             `json:"ExcludedAccounts"`
	ConformancePackInputParameters  []packInputParamJSON `json:"ConformancePackInputParameters"`
}

type putOrgPackResp struct {
	OrganizationConformancePackArn string `json:"OrganizationConformancePackArn,omitempty"`
}

func (h *Handler) putOrganizationConformancePack(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putOrgPackReq) (any, error) {
		pack := cfgdriver.OrganizationConformancePack{
			Name:                req.OrganizationConformancePackName,
			TemplateBody:        req.TemplateBody,
			TemplateS3URI:       req.TemplateS3Uri,
			DeliveryS3Bucket:    req.DeliveryS3Bucket,
			DeliveryS3KeyPrefix: req.DeliveryS3KeyPrefix,
			ExcludedAccounts:    req.ExcludedAccounts,
			InputParameters:     packParamsToMap(req.ConformancePackInputParameters),
		}

		arn, err := h.cfg.PutOrganizationConformancePack(ctx, pack)
		if err != nil {
			return nil, err
		}

		return putOrgPackResp{OrganizationConformancePackArn: arn}, nil
	})
}

type orgPackNamesReq struct {
	OrganizationConformancePackNames []string `json:"OrganizationConformancePackNames"`
	NextToken                        string   `json:"NextToken"`
	Limit                            int32    `json:"Limit"`
}

type orgPackJSON struct {
	OrganizationConformancePackName string   `json:"OrganizationConformancePackName,omitempty"`
	OrganizationConformancePackArn  string   `json:"OrganizationConformancePackArn,omitempty"`
	ExcludedAccounts                []string `json:"ExcludedAccounts,omitempty"`
	LastUpdateTime                  *float64 `json:"LastUpdateTime,omitempty"`
}

type describeOrgPacksResp struct {
	OrganizationConformancePacks []orgPackJSON `json:"OrganizationConformancePacks"`
	NextToken                    string        `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeOrganizationConformancePacks(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgPackNamesReq) (any, error) {
		packs, next, err := h.cfg.DescribeOrganizationConformancePacks(
			ctx, req.OrganizationConformancePackNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]orgPackJSON, 0, len(packs))
		for i := range packs {
			out = append(out, orgPackJSON{
				OrganizationConformancePackName: packs[i].Name,
				OrganizationConformancePackArn:  packs[i].Arn,
				ExcludedAccounts:                packs[i].ExcludedAccounts,
				LastUpdateTime:                  epochOrNil(packs[i].LastUpdateTime),
			})
		}

		return describeOrgPacksResp{OrganizationConformancePacks: out, NextToken: next}, nil
	})
}

type orgPackStatusJSON struct {
	OrganizationConformancePackName string `json:"OrganizationConformancePackName,omitempty"`
	Status                          string `json:"Status,omitempty"`
}

type describeOrgPackStatusesResp struct {
	OrganizationConformancePackStatuses []orgPackStatusJSON `json:"OrganizationConformancePackStatuses"`
	NextToken                           string              `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeOrganizationConformancePackStatuses(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgPackNamesReq) (any, error) {
		packs, next, err := h.cfg.DescribeOrganizationConformancePackStatuses(
			ctx, req.OrganizationConformancePackNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]orgPackStatusJSON, 0, len(packs))
		for i := range packs {
			out = append(out, orgPackStatusJSON{
				OrganizationConformancePackName: packs[i].Name, Status: "CREATE_SUCCESSFUL",
			})
		}

		return describeOrgPackStatusesResp{OrganizationConformancePackStatuses: out, NextToken: next}, nil
	})
}

type orgPackNameReq struct {
	OrganizationConformancePackName string `json:"OrganizationConformancePackName"`
	NextToken                       string `json:"NextToken"`
	Limit                           int32  `json:"Limit"`
}

func (h *Handler) deleteOrganizationConformancePack(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgPackNameReq) (any, error) {
		if err := h.cfg.DeleteOrganizationConformancePack(ctx, req.OrganizationConformancePackName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type orgPackDetailedStatusResp struct {
	OrganizationConformancePackDetailedStatuses []struct {
		AccountID string `json:"AccountId,omitempty"`
	} `json:"OrganizationConformancePackDetailedStatuses,omitempty"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) getOrganizationConformancePackDetailedStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *orgPackNameReq) (any, error) {
		_, next, err := h.cfg.GetOrganizationConformancePackDetailedStatus(
			ctx, req.OrganizationConformancePackName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return orgPackDetailedStatusResp{NextToken: next}, nil
	})
}
