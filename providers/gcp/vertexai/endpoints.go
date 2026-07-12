package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
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
	// Rebalance traffic evenly across every deployed model so each keeps a
	// split entry and the total stays at 100 (real multi-model endpoints never
	// drop a deployed model from the split).
	updated.TrafficSplit = evenTrafficSplit(updated.DeployedModels)
	updated.UpdateTime = m.now()
	m.endpoints.Set(endpoint, updated)

	return m.doneOp(locationOf(endpoint), endpoint), cloneEndpoint(updated), nil
}

// evenTrafficSplit distributes 100% as evenly as possible across the deployed
// models, assigning any rounding remainder to the first model so the split
// always totals exactly 100. Returns an empty (non-nil) map when none remain.
func evenTrafficSplit(models []driver.DeployedModel) map[string]int {
	split := make(map[string]int, len(models))
	if len(models) == 0 {
		return split
	}

	base := 100 / len(models)
	remainder := 100 % len(models)

	for i, dm := range models {
		share := base
		if i < remainder {
			share++
		}

		split[dm.ID] = share
	}

	return split
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
	updated.TrafficSplit = evenTrafficSplit(kept)
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
