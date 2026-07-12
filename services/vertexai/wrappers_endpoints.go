package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func (v *VertexAI) CreateEndpoint(ctx context.Context, cfg driver.EndpointConfig) (*driver.Operation, *driver.Endpoint, error) {
	r, err := cast[opPair[*driver.Endpoint]](v.do(ctx, "CreateEndpoint", cfg, func() (any, error) {
		op, ep, e := v.drv.CreateEndpoint(ctx, cfg)

		return opPair[*driver.Endpoint]{op, ep}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetEndpoint(ctx context.Context, name string) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](v.do(ctx, "GetEndpoint", name, func() (any, error) { return v.drv.GetEndpoint(ctx, name) }))
}

func (v *VertexAI) ListEndpoints(ctx context.Context, location string) ([]driver.Endpoint, error) {
	return cast[[]driver.Endpoint](v.do(ctx, "ListEndpoints", location, func() (any, error) { return v.drv.ListEndpoints(ctx, location) }))
}

func (v *VertexAI) DeleteEndpoint(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteEndpoint", name, func() (any, error) { return v.drv.DeleteEndpoint(ctx, name) }))
}

//nolint:gocritic // dm matches the driver signature; forwarded unchanged.
func (v *VertexAI) DeployModel(
	ctx context.Context, endpoint string, dm driver.DeployedModel,
) (*driver.Operation, *driver.Endpoint, error) {
	r, err := cast[opPair[*driver.Endpoint]](v.do(ctx, "DeployModel", endpoint, func() (any, error) {
		op, ep, e := v.drv.DeployModel(ctx, endpoint, dm)

		return opPair[*driver.Endpoint]{op, ep}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) UndeployModel(
	ctx context.Context, endpoint, deployedModelID string,
) (*driver.Operation, *driver.Endpoint, error) {
	r, err := cast[opPair[*driver.Endpoint]](v.do(ctx, "UndeployModel", endpoint, func() (any, error) {
		op, ep, e := v.drv.UndeployModel(ctx, endpoint, deployedModelID)

		return opPair[*driver.Endpoint]{op, ep}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) Predict(ctx context.Context, req driver.PredictRequest) (*driver.PredictResponse, error) {
	return cast[*driver.PredictResponse](v.do(ctx, "Predict", req, func() (any, error) { return v.drv.Predict(ctx, req) }))
}

func (v *VertexAI) RawPredict(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return cast[[]byte](v.do(ctx, "RawPredict", endpoint, func() (any, error) { return v.drv.RawPredict(ctx, endpoint, body) }))
}
