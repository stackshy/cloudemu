package configservice

import (
	"context"
	"net/http"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

type putPackReq struct {
	ConformancePackName            string               `json:"ConformancePackName"`
	TemplateBody                   string               `json:"TemplateBody"`
	TemplateS3Uri                  string               `json:"TemplateS3Uri"`
	DeliveryS3Bucket               string               `json:"DeliveryS3Bucket"`
	DeliveryS3KeyPrefix            string               `json:"DeliveryS3KeyPrefix"`
	ConformancePackInputParameters []packInputParamJSON `json:"ConformancePackInputParameters"`
}

type putPackResp struct {
	ConformancePackArn string `json:"ConformancePackArn,omitempty"`
}

func (h *Handler) putConformancePack(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putPackReq) (any, error) {
		pack := cfgdriver.ConformancePack{
			ConformancePackName: req.ConformancePackName,
			TemplateBody:        req.TemplateBody,
			TemplateS3URI:       req.TemplateS3Uri,
			DeliveryS3Bucket:    req.DeliveryS3Bucket,
			DeliveryS3KeyPrefix: req.DeliveryS3KeyPrefix,
			InputParameters:     packParamsToMap(req.ConformancePackInputParameters),
		}

		arn, err := h.cfg.PutConformancePack(ctx, pack)
		if err != nil {
			return nil, err
		}

		return putPackResp{ConformancePackArn: arn}, nil
	})
}

func packParamsToMap(in []packInputParamJSON) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, p := range in {
		out[p.ParameterName] = p.ParameterValue
	}

	return out
}

type packNamesReq struct {
	ConformancePackNames []string `json:"ConformancePackNames"`
	NextToken            string   `json:"NextToken"`
	Limit                int32    `json:"Limit"`
}

type describePacksResp struct {
	ConformancePackDetails []conformancePackJSON `json:"ConformancePackDetails"`
	NextToken              string                `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConformancePacks(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNamesReq) (any, error) {
		packs, next, err := h.cfg.DescribeConformancePacks(
			ctx, req.ConformancePackNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]conformancePackJSON, 0, len(packs))
		for i := range packs {
			out = append(out, packToWire(&packs[i]))
		}

		return describePacksResp{ConformancePackDetails: out, NextToken: next}, nil
	})
}

type describePackStatusResp struct {
	ConformancePackStatusDetails []packStatusJSON `json:"ConformancePackStatusDetails"`
	NextToken                    string           `json:"NextToken,omitempty"`
}

func (h *Handler) describeConformancePackStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNamesReq) (any, error) {
		packs, next, err := h.cfg.DescribeConformancePackStatus(
			ctx, req.ConformancePackNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]packStatusJSON, 0, len(packs))
		for i := range packs {
			out = append(out, packStatusJSON{
				ConformancePackArn:      packs[i].ConformancePackArn,
				ConformancePackID:       packs[i].ConformancePackID,
				ConformancePackName:     packs[i].ConformancePackName,
				ConformancePackState:    packs[i].State,
				LastUpdateRequestedTime: epochOrNil(packs[i].LastUpdateRequestedTime),
			})
		}

		return describePackStatusResp{ConformancePackStatusDetails: out, NextToken: next}, nil
	})
}

type packNameReq struct {
	ConformancePackName string `json:"ConformancePackName"`
	NextToken           string `json:"NextToken"`
	Limit               int32  `json:"Limit"`
}

func (h *Handler) deleteConformancePack(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNameReq) (any, error) {
		if err := h.cfg.DeleteConformancePack(ctx, req.ConformancePackName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type packComplianceDetailsResp struct {
	ConformancePackName                  string                 `json:"ConformancePackName,omitempty"`
	ConformancePackRuleEvaluationResults []evaluationResultJSON `json:"ConformancePackRuleEvaluationResults,omitempty"`
	NextToken                            string                 `json:"NextToken,omitempty"`
}

func (h *Handler) getConformancePackComplianceDetails(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNameReq) (any, error) {
		evals, next, err := h.cfg.GetConformancePackComplianceDetails(
			ctx, req.ConformancePackName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return packComplianceDetailsResp{
			ConformancePackName:                  req.ConformancePackName,
			ConformancePackRuleEvaluationResults: evalResults("", evals),
			NextToken:                            next,
		}, nil
	})
}

type packComplianceScoreJSON struct {
	ConformancePackName string   `json:"ConformancePackName,omitempty"`
	Score               string   `json:"Score,omitempty"`
	LastUpdatedTime     *float64 `json:"LastUpdatedTime,omitempty"`
}

type getPackComplianceSummaryResp struct {
	ConformancePackComplianceSummaryList []struct {
		ConformancePackName             string `json:"ConformancePackName,omitempty"`
		ConformancePackComplianceStatus string `json:"ConformancePackComplianceStatus,omitempty"`
	} `json:"ConformancePackComplianceSummaryList"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) getConformancePackComplianceSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNamesReq) (any, error) {
		packs, next, err := h.cfg.GetConformancePackComplianceSummary(
			ctx, req.ConformancePackNames, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		var resp getPackComplianceSummaryResp

		resp.NextToken = next
		for i := range packs {
			resp.ConformancePackComplianceSummaryList = append(resp.ConformancePackComplianceSummaryList, struct {
				ConformancePackName             string `json:"ConformancePackName,omitempty"`
				ConformancePackComplianceStatus string `json:"ConformancePackComplianceStatus,omitempty"`
			}{ConformancePackName: packs[i].ConformancePackName, ConformancePackComplianceStatus: "COMPLIANT"})
		}

		return resp, nil
	})
}

type describePackComplianceResp struct {
	ConformancePackName               string `json:"ConformancePackName,omitempty"`
	ConformancePackRuleComplianceList []struct {
		ConfigRuleName string `json:"ConfigRuleName,omitempty"`
		ComplianceType string `json:"ComplianceType,omitempty"`
	} `json:"ConformancePackRuleComplianceList,omitempty"`
	NextToken string `json:"NextToken,omitempty"`
}

func (h *Handler) describeConformancePackCompliance(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *packNameReq) (any, error) {
		rules, next, err := h.cfg.DescribeConformancePackCompliance(
			ctx, req.ConformancePackName, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		var resp describePackComplianceResp

		resp.ConformancePackName = req.ConformancePackName
		resp.NextToken = next

		for i := range rules {
			resp.ConformancePackRuleComplianceList = append(resp.ConformancePackRuleComplianceList, struct {
				ConfigRuleName string `json:"ConfigRuleName,omitempty"`
				ComplianceType string `json:"ComplianceType,omitempty"`
			}{ConfigRuleName: rules[i].ConfigRuleName, ComplianceType: rules[i].Compliance})
		}

		return resp, nil
	})
}

type listPackScoresResp struct {
	ConformancePackComplianceScores []packComplianceScoreJSON `json:"ConformancePackComplianceScores"`
	NextToken                       string                    `json:"NextToken,omitempty"`
}

func (h *Handler) listConformancePackComplianceScores(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		packs, next, err := h.cfg.ListConformancePackComplianceScores(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]packComplianceScoreJSON, 0, len(packs))
		for i := range packs {
			out = append(out, packComplianceScoreJSON{
				ConformancePackName: packs[i].ConformancePackName,
				Score:               "100.00",
				LastUpdatedTime:     epochOrNil(packs[i].LastUpdateRequestedTime),
			})
		}

		return listPackScoresResp{ConformancePackComplianceScores: out, NextToken: next}, nil
	})
}
