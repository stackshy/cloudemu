package ssm

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func (h *Handler) putParameter(w http.ResponseWriter, r *http.Request) {
	var req putParameterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	version, tier, err := h.store.PutParameter(r.Context(), ssmdriver.PutConfig{
		Name:        req.Name,
		Value:       req.Value,
		Type:        req.Type,
		Description: req.Description,
		Overwrite:   req.Overwrite,
		Tier:        req.Tier,
		DataType:    req.DataType,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putParameterResponse{Tier: tier, Version: version})
}

func (h *Handler) getParameter(w http.ResponseWriter, r *http.Request) {
	var req getParameterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.store.GetParameter(r.Context(), req.Name, req.WithDecryption)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, getParameterResponse{Parameter: toParameterJSON(*p)})
}

func (h *Handler) getParameters(w http.ResponseWriter, r *http.Request) {
	var req getParametersRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	found, invalid, err := h.store.GetParameters(r.Context(), req.Names, req.WithDecryption)
	if err != nil {
		writeErr(w, err)
		return
	}

	params := make([]parameterJSON, 0, len(found))
	for _, p := range found {
		params = append(params, toParameterJSON(p))
	}

	wire.WriteJSON(w, getParametersResponse{Parameters: params, InvalidParameters: invalid})
}

func (h *Handler) getParametersByPath(w http.ResponseWriter, r *http.Request) {
	var req getParametersByPathRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	found, err := h.store.GetParametersByPath(r.Context(), ssmdriver.GetByPathInput{
		Path:           req.Path,
		Recursive:      req.Recursive,
		WithDecryption: req.WithDecryption,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	params := make([]parameterJSON, 0, len(found))
	for _, p := range found {
		params = append(params, toParameterJSON(p))
	}

	wire.WriteJSON(w, getParametersByPathResponse{Parameters: params})
}

func (h *Handler) deleteParameter(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.store.DeleteParameter(r.Context(), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) deleteParameters(w http.ResponseWriter, r *http.Request) {
	var req namesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	deleted, invalid, err := h.store.DeleteParameters(r.Context(), req.Names)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, deleteParametersResponse{DeletedParameters: deleted, InvalidParameters: invalid})
}

func (h *Handler) describeParameters(w http.ResponseWriter, r *http.Request) {
	metas, err := h.store.DescribeParameters(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]parameterMetadataJSON, 0, len(metas))
	for _, md := range metas {
		out = append(out, parameterMetadataJSON{
			ARN:              md.ARN,
			DataType:         md.DataType,
			Description:      md.Description,
			LastModifiedDate: epochSeconds(md.LastModified),
			LastModifiedUser: md.LastModifiedUser,
			Name:             md.Name,
			Tier:             md.Tier,
			Type:             md.Type,
			Version:          md.Version,
		})
	}

	wire.WriteJSON(w, describeParametersResponse{Parameters: out})
}

func (h *Handler) getParameterHistory(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	history, err := h.store.GetParameterHistory(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]parameterHistoryJSON, 0, len(history))
	for _, p := range history {
		out = append(out, parameterHistoryJSON{
			ARN:              p.ARN,
			DataType:         p.DataType,
			LastModifiedDate: epochSeconds(p.LastModified),
			Name:             p.Name,
			Type:             p.Type,
			Value:            p.Value,
			Version:          p.Version,
		})
	}

	wire.WriteJSON(w, getParameterHistoryResponse{Parameters: out})
}

func (h *Handler) labelParameterVersion(w http.ResponseWriter, r *http.Request) {
	var req labelParameterVersionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	applied, invalid, err := h.store.LabelParameterVersion(r.Context(), req.Name, req.ParameterVersion, req.Labels)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, labelParameterVersionResponse{InvalidLabels: invalid, ParameterVersion: applied})
}
