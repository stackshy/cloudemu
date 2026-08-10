package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// --- security configurations ---

type securityConfigurationJSON struct {
	Name                    string         `json:"Name"`
	CreatedTimeStamp        *float64       `json:"CreatedTimeStamp,omitempty"`
	EncryptionConfiguration map[string]any `json:"EncryptionConfiguration,omitempty"`
}

func secConfigToWire(sc *driver.SecurityConfiguration) securityConfigurationJSON {
	return securityConfigurationJSON{
		Name: sc.Name, CreatedTimeStamp: epochOrNil(sc.CreatedTimeStamp),
		EncryptionConfiguration: sc.EncryptionConfig,
	}
}

type createSecurityConfigurationRequest struct {
	Name                    string         `json:"Name"`
	EncryptionConfiguration map[string]any `json:"EncryptionConfiguration"`
}

type createSecurityConfigurationResponse struct {
	Name             string   `json:"Name,omitempty"`
	CreatedTimestamp *float64 `json:"CreatedTimestamp,omitempty"`
}

func (h *Handler) createSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createSecurityConfigurationRequest) (any, error) {
		if err := h.glue.CreateSecurityConfiguration(ctx, driver.SecurityConfiguration{
			Name: req.Name, EncryptionConfig: req.EncryptionConfiguration,
		}); err != nil {
			return nil, err
		}

		sc, err := h.glue.GetSecurityConfiguration(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return createSecurityConfigurationResponse{Name: sc.Name, CreatedTimestamp: epochOrNil(sc.CreatedTimeStamp)}, nil
	})
}

type secConfigNameRequest struct {
	Name string `json:"Name"`
}

type getSecurityConfigurationResponse struct {
	SecurityConfiguration securityConfigurationJSON `json:"SecurityConfiguration"`
}

func (h *Handler) getSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *secConfigNameRequest) (any, error) {
		sc, err := h.glue.GetSecurityConfiguration(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getSecurityConfigurationResponse{SecurityConfiguration: secConfigToWire(sc)}, nil
	})
}

func (h *Handler) deleteSecurityConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *secConfigNameRequest) (any, error) {
		if err := h.glue.DeleteSecurityConfiguration(ctx, req.Name); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getSecurityConfigurationsResponse struct {
	SecurityConfigurations []securityConfigurationJSON `json:"SecurityConfigurations"`
	NextToken              string                      `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getSecurityConfigurations(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		scs, next, err := h.glue.GetSecurityConfigurations(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]securityConfigurationJSON, 0, len(scs))
		for i := range scs {
			out = append(out, secConfigToWire(&scs[i]))
		}

		return getSecurityConfigurationsResponse{SecurityConfigurations: out, NextToken: next}, nil
	})
}

// --- dev endpoints ---

type devEndpointJSON struct {
	EndpointName          string            `json:"EndpointName"`
	RoleArn               string            `json:"RoleArn,omitempty"`
	Status                string            `json:"Status,omitempty"`
	WorkerType            string            `json:"WorkerType,omitempty"`
	GlueVersion           string            `json:"GlueVersion,omitempty"`
	NumberOfWorkers       int32             `json:"NumberOfWorkers,omitempty"`
	Arguments             map[string]string `json:"Arguments,omitempty"`
	CreatedTimestamp      *float64          `json:"CreatedTimestamp,omitempty"`
	LastModifiedTimestamp *float64          `json:"LastModifiedTimestamp,omitempty"`
	PublicAddress         string            `json:"PublicAddress,omitempty"`
}

func devEndpointToWire(e *driver.DevEndpoint) devEndpointJSON {
	return devEndpointJSON{
		EndpointName: e.EndpointName, RoleArn: e.RoleARN, Status: e.Status, WorkerType: e.WorkerType,
		GlueVersion: e.GlueVersion, NumberOfWorkers: e.NumberOfWorkers, Arguments: e.Arguments,
		CreatedTimestamp: epochOrNil(e.CreatedTimestamp), LastModifiedTimestamp: epochOrNil(e.LastModifiedTimestamp),
		PublicAddress: e.PublicAddress,
	}
}

type createDevEndpointRequest struct {
	EndpointName    string            `json:"EndpointName"`
	RoleArn         string            `json:"RoleArn"`
	WorkerType      string            `json:"WorkerType"`
	GlueVersion     string            `json:"GlueVersion"`
	NumberOfWorkers int32             `json:"NumberOfWorkers"`
	Arguments       map[string]string `json:"Arguments"`
}

func (h *Handler) createDevEndpoint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createDevEndpointRequest) (any, error) {
		e, err := h.glue.CreateDevEndpoint(ctx, driver.DevEndpoint{
			EndpointName: req.EndpointName, RoleARN: req.RoleArn, WorkerType: req.WorkerType,
			GlueVersion: req.GlueVersion, NumberOfWorkers: req.NumberOfWorkers, Arguments: req.Arguments,
		})
		if err != nil {
			return nil, err
		}

		return devEndpointToWire(e), nil
	})
}

type devEndpointNameRequest struct {
	EndpointName string `json:"EndpointName"`
}

type getDevEndpointResponse struct {
	DevEndpoint devEndpointJSON `json:"DevEndpoint"`
}

func (h *Handler) getDevEndpoint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *devEndpointNameRequest) (any, error) {
		e, err := h.glue.GetDevEndpoint(ctx, req.EndpointName)
		if err != nil {
			return nil, err
		}

		return getDevEndpointResponse{DevEndpoint: devEndpointToWire(e)}, nil
	})
}

type updateDevEndpointRequest struct {
	EndpointName string            `json:"EndpointName"`
	AddArguments map[string]string `json:"AddArguments"`
}

func (h *Handler) updateDevEndpoint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateDevEndpointRequest) (any, error) {
		if err := h.glue.UpdateDevEndpoint(ctx, req.EndpointName, req.AddArguments); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) deleteDevEndpoint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *devEndpointNameRequest) (any, error) {
		if err := h.glue.DeleteDevEndpoint(ctx, req.EndpointName); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getDevEndpointsResponse struct {
	DevEndpoints []devEndpointJSON `json:"DevEndpoints"`
	NextToken    string            `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getDevEndpoints(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		es, next, err := h.glue.GetDevEndpoints(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]devEndpointJSON, 0, len(es))
		for i := range es {
			out = append(out, devEndpointToWire(&es[i]))
		}

		return getDevEndpointsResponse{DevEndpoints: out, NextToken: next}, nil
	})
}

type listDevEndpointsResponse struct {
	DevEndpointNames []string `json:"DevEndpointNames"`
	NextToken        string   `json:"NextToken,omitempty"`
}

func (h *Handler) listDevEndpoints(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListDevEndpoints(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listDevEndpointsResponse{DevEndpointNames: names, NextToken: next}, nil
	})
}

type batchGetDevEndpointsRequest struct {
	DevEndpointNames []string `json:"DevEndpointNames"`
}

type batchGetDevEndpointsResponse struct {
	DevEndpoints         []devEndpointJSON `json:"DevEndpoints"`
	DevEndpointsNotFound []string          `json:"DevEndpointsNotFound,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetDevEndpoints(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetDevEndpointsRequest) (any, error) {
		found, notFound, err := h.glue.BatchGetDevEndpoints(ctx, req.DevEndpointNames)
		if err != nil {
			return nil, err
		}

		out := make([]devEndpointJSON, 0, len(found))
		for i := range found {
			out = append(out, devEndpointToWire(&found[i]))
		}

		return batchGetDevEndpointsResponse{DevEndpoints: out, DevEndpointsNotFound: notFound}, nil
	})
}
