package configservice

import (
	"context"
	"net/http"
)

type putRemediationReq struct {
	RemediationConfigurations []remediationConfigJSON `json:"RemediationConfigurations"`
}

type putRemediationResp struct {
	FailedBatches []struct{} `json:"FailedBatches,omitempty"`
}

func (h *Handler) putRemediationConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRemediationReq) (any, error) {
		cfgs := make([]cfgRemediationConfig, 0, len(req.RemediationConfigurations))
		for i := range req.RemediationConfigurations {
			cfgs = append(cfgs, req.RemediationConfigurations[i].toDriver())
		}

		if _, err := h.cfg.PutRemediationConfigurations(ctx, cfgs); err != nil {
			return nil, err
		}

		return putRemediationResp{}, nil
	})
}

type describeRemediationReq struct {
	ConfigRuleNames []string `json:"ConfigRuleNames"`
}

type describeRemediationResp struct {
	RemediationConfigurations []remediationConfigJSON `json:"RemediationConfigurations"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeRemediationConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeRemediationReq) (any, error) {
		cfgs, err := h.cfg.DescribeRemediationConfigurations(ctx, req.ConfigRuleNames)
		if err != nil {
			return nil, err
		}

		out := make([]remediationConfigJSON, 0, len(cfgs))
		for i := range cfgs {
			out = append(out, remediationToWire(&cfgs[i]))
		}

		return describeRemediationResp{RemediationConfigurations: out}, nil
	})
}

type deleteRemediationReq struct {
	ConfigRuleName string `json:"ConfigRuleName"`
	ResourceType   string `json:"ResourceType"`
}

func (h *Handler) deleteRemediationConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteRemediationReq) (any, error) {
		if err := h.cfg.DeleteRemediationConfiguration(ctx, req.ConfigRuleName, req.ResourceType); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type putRemExceptionsReq struct {
	ConfigRuleName string            `json:"ConfigRuleName"`
	ResourceKeys   []resourceKeyJSON `json:"ResourceKeys"`
	Message        string            `json:"Message"`
}

func (h *Handler) putRemediationExceptions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRemExceptionsReq) (any, error) {
		exceptions := make([]cfgRemediationException, 0, len(req.ResourceKeys))
		for _, k := range req.ResourceKeys {
			exceptions = append(exceptions, cfgRemediationException{
				ResourceType: k.ResourceType, ResourceID: k.ResourceID, Message: req.Message,
			})
		}

		if _, err := h.cfg.PutRemediationExceptions(ctx, req.ConfigRuleName, exceptions); err != nil {
			return nil, err
		}

		return map[string]any{"FailedBatches": []struct{}{}}, nil
	})
}

type remExceptionsReq struct {
	ConfigRuleName string            `json:"ConfigRuleName"`
	ResourceKeys   []resourceKeyJSON `json:"ResourceKeys"`
	NextToken      string            `json:"NextToken"`
	Limit          int32             `json:"Limit"`
}

type describeRemExceptionsResp struct {
	RemediationExceptions []remediationExceptionJSON `json:"RemediationExceptions"`
	NextToken             string                     `json:"NextToken,omitempty"`
}

func (h *Handler) describeRemediationExceptions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *remExceptionsReq) (any, error) {
		keys := driverKeys(req.ResourceKeys)

		exs, next, err := h.cfg.DescribeRemediationExceptions(
			ctx, req.ConfigRuleName, keys, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]remediationExceptionJSON, 0, len(exs))
		for i := range exs {
			out = append(out, remExceptionToWire(&exs[i]))
		}

		return describeRemExceptionsResp{RemediationExceptions: out, NextToken: next}, nil
	})
}

type deleteRemExceptionsReq struct {
	ConfigRuleName string            `json:"ConfigRuleName"`
	ResourceKeys   []resourceKeyJSON `json:"ResourceKeys"`
}

func (h *Handler) deleteRemediationExceptions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteRemExceptionsReq) (any, error) {
		failed, err := h.cfg.DeleteRemediationExceptions(ctx, req.ConfigRuleName, driverKeys(req.ResourceKeys))
		if err != nil {
			return nil, err
		}

		fb := make([]map[string]any, 0, len(failed))
		for _, k := range failed {
			fb = append(fb, map[string]any{
				"FailureMessage": "not found",
				"FailedItems":    []resourceKeyJSON{resourceKeyToWire(k)},
			})
		}

		return map[string]any{"FailedBatches": fb}, nil
	})
}

type remExecStatusReq struct {
	ConfigRuleName string            `json:"ConfigRuleName"`
	ResourceKeys   []resourceKeyJSON `json:"ResourceKeys"`
	NextToken      string            `json:"NextToken"`
	Limit          int32             `json:"Limit"`
}

func (h *Handler) describeRemediationExecutionStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *remExecStatusReq) (any, error) {
		_, next, err := h.cfg.DescribeRemediationExecutionStatus(
			ctx, req.ConfigRuleName, driverKeys(req.ResourceKeys), pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		return map[string]any{"RemediationExecutionStatuses": []struct{}{}, "NextToken": next}, nil
	})
}

type startRemediationReq struct {
	ConfigRuleName string            `json:"ConfigRuleName"`
	ResourceKeys   []resourceKeyJSON `json:"ResourceKeys"`
}

func (h *Handler) startRemediationExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startRemediationReq) (any, error) {
		failed, err := h.cfg.StartRemediationExecution(ctx, req.ConfigRuleName, driverKeys(req.ResourceKeys))
		if err != nil {
			return nil, err
		}

		fk := make([]resourceKeyJSON, 0, len(failed))
		for _, k := range failed {
			fk = append(fk, resourceKeyToWire(k))
		}

		return map[string]any{"FailureMessage": "", "FailedItems": fk}, nil
	})
}

func driverKeys(in []resourceKeyJSON) []cfgResourceKey {
	out := make([]cfgResourceKey, 0, len(in))
	for _, k := range in {
		out = append(out, k.toDriver())
	}

	return out
}
