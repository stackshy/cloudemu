package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

func (m *Mock) CreateEndpoint(_ context.Context, cfg driver.EndpointConfig) (*driver.Operation, *driver.Endpoint, error) {
	now := m.now()
	name := m.resName(cfg.Location, "endpoints", m.newID())
	ep := &driver.Endpoint{
		Name:         name,
		DisplayName:  cfg.DisplayName,
		Description:  cfg.Description,
		TrafficSplit: map[string]int{},
		Labels:       copyLabels(cfg.Labels),
		CreateTime:   now,
		UpdateTime:   now,
	}
	m.endpoints.Set(name, ep)
	m.emitMetric("endpoint/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	return m.doneOp(cfg.Location, name), cloneEndpoint(ep), nil
}

func (m *Mock) GetEndpoint(_ context.Context, name string) (*driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	return cloneEndpoint(ep), nil
}

func (m *Mock) ListEndpoints(_ context.Context, location string) ([]driver.Endpoint, error) {
	out := make([]driver.Endpoint, 0)

	for _, ep := range m.endpoints.All() {
		if location == "" || locationOf(ep.Name) == location {
			out = append(out, *cloneEndpoint(ep))
		}
	}

	return out, nil
}

func (m *Mock) DeleteEndpoint(_ context.Context, name string) (*driver.Operation, error) {
	if !m.endpoints.Has(name) {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", name)
	}

	m.endpoints.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

//nolint:gocritic // dm matches the driver signature; copied on entry.
func (m *Mock) DeployModel(_ context.Context, endpoint string, dm driver.DeployedModel) (*driver.Operation, *driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(endpoint)
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "endpoint %q not found", endpoint)
	}

	if dm.ID == "" {
		dm.ID = m.newID()
	}

	// Deep-copy working set so the stored endpoint is never mutated in place.
	updated := cloneEndpoint(ep)
	updated.DeployedModels = append(updated.DeployedModels, dm)
	updated.TrafficSplit = map[string]int{dm.ID: 100}
	updated.UpdateTime = m.now()
	m.endpoints.Set(endpoint, updated)

	return m.doneOp(locationOf(endpoint), endpoint), cloneEndpoint(updated), nil
}

func (m *Mock) UndeployModel(_ context.Context, endpoint, deployedModelID string) (*driver.Operation, *driver.Endpoint, error) {
	ep, ok := m.endpoints.Get(endpoint)
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "endpoint %q not found", endpoint)
	}

	updated := cloneEndpoint(ep)

	kept := make([]driver.DeployedModel, 0, len(updated.DeployedModels))

	for _, d := range updated.DeployedModels {
		if d.ID != deployedModelID {
			kept = append(kept, d)
		}
	}

	updated.DeployedModels = kept
	delete(updated.TrafficSplit, deployedModelID)
	updated.UpdateTime = m.now()
	m.endpoints.Set(endpoint, updated)

	return m.doneOp(locationOf(endpoint), endpoint), cloneEndpoint(updated), nil
}

// Predict echoes the request instances back as predictions, which is
// deterministic and adequate for exercising client serialization.
//

func (m *Mock) Predict(_ context.Context, req driver.PredictRequest) (*driver.PredictResponse, error) {
	ep, ok := m.endpoints.Get(req.Endpoint)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", req.Endpoint)
	}

	m.emitMetric("prediction/count", float64(len(req.Instances)), map[string]string{"endpoint": req.Endpoint})

	resp := &driver.PredictResponse{Predictions: append([]any{}, req.Instances...)}
	if len(ep.DeployedModels) > 0 {
		resp.DeployedModelID = ep.DeployedModels[0].ID
		resp.Model = ep.DeployedModels[0].Model
		resp.ModelDisplayName = ep.DeployedModels[0].DisplayName
	}

	return resp, nil
}

func (m *Mock) RawPredict(_ context.Context, endpoint string, body []byte) ([]byte, error) {
	if !m.endpoints.Has(endpoint) {
		return nil, errors.Newf(errors.NotFound, "endpoint %q not found", endpoint)
	}

	m.emitMetric("prediction/count", 1, map[string]string{"endpoint": endpoint})

	out := make([]byte, len(body))
	copy(out, body)

	return out, nil
}
