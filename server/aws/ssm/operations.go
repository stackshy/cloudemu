package ssm

import (
	"errors"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func (h *Handler) putParameter(w http.ResponseWriter, r *http.Request) {
	var req putParameterRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	var tags map[string]string
	if len(req.Tags) > 0 {
		tags = make(map[string]string, len(req.Tags))
		for _, t := range req.Tags {
			tags[t.Key] = t.Value
		}
	}

	version, tier, err := h.store.PutParameter(r.Context(), ssmdriver.PutConfig{
		Name:        req.Name,
		Value:       req.Value,
		Type:        req.Type,
		Description: req.Description,
		Overwrite:   req.Overwrite,
		Tier:        req.Tier,
		DataType:    req.DataType,
		Tags:        tags,
	})
	if err != nil {
		// Changing a parameter's type on an Overwrite update is rejected by
		// real Parameter Store with HierarchyTypeMismatchException, not the
		// generic ValidationException.
		if errors.Is(err, ssmdriver.ErrTypeMismatch) {
			wire.WriteJSONError(w, http.StatusBadRequest, "HierarchyTypeMismatchException", err.Error())
			return
		}

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
		// The parameter existed but the requested version/label didn't — AWS
		// returns the distinct ParameterVersionNotFound, not ParameterNotFound.
		if errors.Is(err, ssmdriver.ErrVersionNotFound) {
			wire.WriteJSONError(w, http.StatusBadRequest, "ParameterVersionNotFound", err.Error())
			return
		}
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

	// The driver returns the full matching set sorted by name; paginate here so
	// MaxResults/NextToken (and the SDK's paginator) work over a stable order.
	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsByPath, len(found))
	if err != nil {
		writeErr(w, err)
		return
	}

	params := make([]parameterJSON, 0, end-start)
	for _, p := range found[start:end] {
		params = append(params, toParameterJSON(p))
	}

	wire.WriteJSON(w, getParametersByPathResponse{Parameters: params, NextToken: next})
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
	var req describeParametersRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	metas, err := h.store.DescribeParameters(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	// Apply ParameterFilters/Filters before paginating so the window is over the
	// filtered set (and NextToken offsets stay stable).
	if len(req.ParameterFilters) > 0 || len(req.Filters) > 0 {
		filtered := metas[:0:0]

		for i := range metas {
			if matchesParameterFilters(&metas[i], req.ParameterFilters, req.Filters) {
				filtered = append(filtered, metas[i])
			}
		}

		metas = filtered
	}

	// Sorted-by-name from the driver; paginate here (MaxResults/NextToken).
	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsDescribe, len(metas))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]parameterMetadataJSON, 0, end-start)
	for _, md := range metas[start:end] {
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

	wire.WriteJSON(w, describeParametersResponse{Parameters: out, NextToken: next})
}

func (h *Handler) getParameterHistory(w http.ResponseWriter, r *http.Request) {
	var req getParameterHistoryRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	history, err := h.store.GetParameterHistory(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	// The driver returns versions oldest-first; paginate over that stable order.
	start, end, next, err := pageWindow(req.NextToken, req.MaxResults, maxResultsHistory, len(history))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]parameterHistoryJSON, 0, end-start)
	for _, p := range history[start:end] {
		out = append(out, parameterHistoryJSON{
			ARN:              p.ARN,
			DataType:         p.DataType,
			Description:      p.Description,
			Labels:           p.Labels,
			LastModifiedDate: epochSeconds(p.LastModified),
			LastModifiedUser: p.LastModifiedUser,
			Name:             p.Name,
			Tier:             p.Tier,
			Type:             p.Type,
			Value:            p.Value,
			Version:          p.Version,
		})
	}

	wire.WriteJSON(w, getParameterHistoryResponse{Parameters: out, NextToken: next})
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
