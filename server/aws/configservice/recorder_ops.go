package configservice

import (
	"context"
	"net/http"
)

type putRecorderReq struct {
	ConfigurationRecorder *configurationRecorderJSON `json:"ConfigurationRecorder"`
	Tags                  []tag                      `json:"Tags"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) putConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putRecorderReq) (any, error) {
		if req.ConfigurationRecorder == nil {
			return nil, invalidRequest("ConfigurationRecorder is required")
		}

		rec := req.ConfigurationRecorder.toDriver()
		rec.Tags = tagsToMap(req.Tags)

		if err := h.cfg.PutConfigurationRecorder(ctx, rec); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type namesReq struct {
	ConfigurationRecorderNames []string `json:"ConfigurationRecorderNames"`
	Arn                        string   `json:"Arn"`
}

type describeRecordersResp struct {
	ConfigurationRecorders []configurationRecorderJSON `json:"ConfigurationRecorders"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConfigurationRecorders(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *namesReq) (any, error) {
		recs, err := h.cfg.DescribeConfigurationRecorders(ctx, req.ConfigurationRecorderNames)
		if err != nil {
			return nil, err
		}

		out := make([]configurationRecorderJSON, 0, len(recs))
		for i := range recs {
			out = append(out, recorderToWire(&recs[i]))
		}

		return describeRecordersResp{ConfigurationRecorders: out}, nil
	})
}

type describeRecorderStatusResp struct {
	ConfigurationRecordersStatus []recorderStatusJSON `json:"ConfigurationRecordersStatus"`
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (h *Handler) describeConfigurationRecorderStatus(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *namesReq) (any, error) {
		recs, err := h.cfg.DescribeConfigurationRecorderStatus(ctx, req.ConfigurationRecorderNames)
		if err != nil {
			return nil, err
		}

		out := make([]recorderStatusJSON, 0, len(recs))
		for i := range recs {
			out = append(out, recorderStatusToWire(&recs[i]))
		}

		return describeRecorderStatusResp{ConfigurationRecordersStatus: out}, nil
	})
}

type recorderNameReq struct {
	ConfigurationRecorderName string `json:"ConfigurationRecorderName"`
}

func (h *Handler) deleteConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *recorderNameReq) (any, error) {
		if err := h.cfg.DeleteConfigurationRecorder(ctx, req.ConfigurationRecorderName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) startConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *recorderNameReq) (any, error) {
		if err := h.cfg.StartConfigurationRecorder(ctx, req.ConfigurationRecorderName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) stopConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *recorderNameReq) (any, error) {
		if err := h.cfg.StopConfigurationRecorder(ctx, req.ConfigurationRecorderName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type pageReq struct {
	NextToken string `json:"NextToken"`
	Limit     int32  `json:"Limit"`
}

type listRecordersResp struct {
	ConfigurationRecorderSummaries []recorderSummaryJSON `json:"ConfigurationRecorderSummaries"`
	NextToken                      string                `json:"NextToken,omitempty"`
}

type recorderSummaryJSON struct {
	Arn              string `json:"arn,omitempty"`
	Name             string `json:"name,omitempty"`
	RecordingScope   string `json:"recordingScope,omitempty"`
	ServicePrincipal string `json:"servicePrincipal,omitempty"`
}

func (h *Handler) listConfigurationRecorders(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *pageReq) (any, error) {
		recs, next, err := h.cfg.ListConfigurationRecorders(ctx, pageFrom(req.NextToken, req.Limit))
		if err != nil {
			return nil, err
		}

		out := make([]recorderSummaryJSON, 0, len(recs))
		for i := range recs {
			out = append(out, recorderSummaryJSON{Arn: recs[i].Arn, Name: recs[i].Name, RecordingScope: "INTERNAL"})
		}

		return listRecordersResp{ConfigurationRecorderSummaries: out, NextToken: next}, nil
	})
}

type serviceLinkedRecorderReq struct {
	ServicePrincipal string `json:"ServicePrincipal"`
	Tags             []tag  `json:"Tags"`
}

type serviceLinkedRecorderResp struct {
	Arn  string `json:"Arn,omitempty"`
	Name string `json:"Name,omitempty"`
}

func (h *Handler) putServiceLinkedConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *serviceLinkedRecorderReq) (any, error) {
		arn, name, err := h.cfg.PutServiceLinkedConfigurationRecorder(ctx, req.ServicePrincipal, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return serviceLinkedRecorderResp{Arn: arn, Name: name}, nil
	})
}

func (h *Handler) putThirdPartyServiceLinkedConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *serviceLinkedRecorderReq) (any, error) {
		arn, name, err := h.cfg.PutThirdPartyServiceLinkedConfigurationRecorder(
			ctx, req.ServicePrincipal, tagsToMap(req.Tags))
		if err != nil {
			return nil, err
		}

		return serviceLinkedRecorderResp{Arn: arn, Name: name}, nil
	})
}

func (h *Handler) deleteServiceLinkedConfigurationRecorder(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *recorderNameReq) (any, error) {
		arn, name, err := h.cfg.DeleteServiceLinkedConfigurationRecorder(ctx, req.ConfigurationRecorderName)
		if err != nil {
			return nil, err
		}

		return serviceLinkedRecorderResp{Arn: arn, Name: name}, nil
	})
}

type associateTypesReq struct {
	ConfigurationRecorderArn string   `json:"ConfigurationRecorderArn"`
	ResourceTypes            []string `json:"ResourceTypes"`
}

type associateTypesResp struct {
	ConfigurationRecorder configurationRecorderJSON `json:"ConfigurationRecorder"`
}

func (h *Handler) associateResourceTypes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *associateTypesReq) (any, error) {
		rec, err := h.cfg.AssociateResourceTypes(ctx, req.ConfigurationRecorderArn, req.ResourceTypes)
		if err != nil {
			return nil, err
		}

		return associateTypesResp{ConfigurationRecorder: recorderToWire(&rec)}, nil
	})
}

func (h *Handler) disassociateResourceTypes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *associateTypesReq) (any, error) {
		rec, err := h.cfg.DisassociateResourceTypes(ctx, req.ConfigurationRecorderArn, req.ResourceTypes)
		if err != nil {
			return nil, err
		}

		return associateTypesResp{ConfigurationRecorder: recorderToWire(&rec)}, nil
	})
}
