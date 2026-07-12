package vertexai

import (
	"context"
	"strconv"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func (m *Mock) CreateIndex(_ context.Context, cfg driver.IndexConfig) (*driver.Operation, *driver.Index, error) {
	now := m.now()
	name := m.resName(cfg.Location, "indexes", m.newID())
	idx := &driver.Index{
		Name: name, DisplayName: cfg.DisplayName, Description: cfg.Description,
		Dimensions: cfg.Dimensions, CreateTime: now, UpdateTime: now,
	}
	m.indexes.Set(name, idx)

	out := *idx

	return m.doneOp(cfg.Location, name), &out, nil
}

func (m *Mock) GetIndex(_ context.Context, name string) (*driver.Index, error) {
	idx, ok := m.indexes.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "index %q not found", name)
	}

	out := *idx

	return &out, nil
}

func (m *Mock) ListIndexes(_ context.Context, location string) ([]driver.Index, error) {
	out := make([]driver.Index, 0)

	for _, idx := range m.indexes.All() {
		if location == "" || locationOf(idx.Name) == location {
			out = append(out, *idx)
		}
	}

	return out, nil
}

func (m *Mock) DeleteIndex(_ context.Context, name string) (*driver.Operation, error) {
	if !m.indexes.Has(name) {
		return nil, errors.Newf(errors.NotFound, "index %q not found", name)
	}

	m.indexes.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) UpsertDatapoints(_ context.Context, index string, datapoints []driver.Datapoint) error {
	idx, ok := m.indexes.Get(index)
	if !ok {
		return errors.Newf(errors.NotFound, "index %q not found", index)
	}

	updated := *idx
	updated.DatapointCount += len(datapoints)
	updated.UpdateTime = m.now()
	m.indexes.Set(index, &updated)

	return nil
}

func (m *Mock) RemoveDatapoints(_ context.Context, index string, datapointIDs []string) error {
	idx, ok := m.indexes.Get(index)
	if !ok {
		return errors.Newf(errors.NotFound, "index %q not found", index)
	}

	updated := *idx
	updated.DatapointCount = maxZero(updated.DatapointCount - len(datapointIDs))
	updated.UpdateTime = m.now()
	m.indexes.Set(index, &updated)

	return nil
}

func maxZero(v int) int {
	if v < 0 {
		return 0
	}

	return v
}

func (m *Mock) CreateIndexEndpoint(_ context.Context, cfg driver.IndexEndpointConfig) (*driver.Operation, *driver.IndexEndpoint, error) {
	name := m.resName(cfg.Location, "indexEndpoints", m.newID())
	ie := &driver.IndexEndpoint{Name: name, DisplayName: cfg.DisplayName, Description: cfg.Description, CreateTime: m.now()}
	m.indexEndpoints.Set(name, ie)

	out := *ie

	return m.doneOp(cfg.Location, name), &out, nil
}

func (m *Mock) GetIndexEndpoint(_ context.Context, name string) (*driver.IndexEndpoint, error) {
	ie, ok := m.indexEndpoints.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "index endpoint %q not found", name)
	}

	return cloneIndexEndpoint(ie), nil
}

func (m *Mock) ListIndexEndpoints(_ context.Context, location string) ([]driver.IndexEndpoint, error) {
	out := make([]driver.IndexEndpoint, 0)

	for _, ie := range m.indexEndpoints.All() {
		if location == "" || locationOf(ie.Name) == location {
			out = append(out, *cloneIndexEndpoint(ie))
		}
	}

	return out, nil
}

func (m *Mock) DeleteIndexEndpoint(_ context.Context, name string) (*driver.Operation, error) {
	if !m.indexEndpoints.Has(name) {
		return nil, errors.Newf(errors.NotFound, "index endpoint %q not found", name)
	}

	m.indexEndpoints.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) DeployIndex(
	_ context.Context, indexEndpoint string, di driver.DeployedIndex,
) (*driver.Operation, *driver.IndexEndpoint, error) {
	ie, ok := m.indexEndpoints.Get(indexEndpoint)
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "index endpoint %q not found", indexEndpoint)
	}

	if di.ID == "" {
		di.ID = m.newID()
	}

	updated := cloneIndexEndpoint(ie)
	updated.DeployedIndexes = append(updated.DeployedIndexes, di)
	m.indexEndpoints.Set(indexEndpoint, updated)

	return m.doneOp(locationOf(indexEndpoint), indexEndpoint), cloneIndexEndpoint(updated), nil
}

func (m *Mock) UndeployIndex(_ context.Context, indexEndpoint, deployedIndexID string) (*driver.Operation, *driver.IndexEndpoint, error) {
	ie, ok := m.indexEndpoints.Get(indexEndpoint)
	if !ok {
		return nil, nil, errors.Newf(errors.NotFound, "index endpoint %q not found", indexEndpoint)
	}

	updated := cloneIndexEndpoint(ie)

	kept := make([]driver.DeployedIndex, 0, len(updated.DeployedIndexes))

	for _, d := range updated.DeployedIndexes {
		if d.ID != deployedIndexID {
			kept = append(kept, d)
		}
	}

	updated.DeployedIndexes = kept
	m.indexEndpoints.Set(indexEndpoint, updated)

	return m.doneOp(locationOf(indexEndpoint), indexEndpoint), cloneIndexEndpoint(updated), nil
}

// maxNeighbors bounds the synthesized neighbor count so an unvalidated (or
// hostile) request can't trigger an unbounded allocation.
const maxNeighbors = 1000

// FindNeighbors returns deterministic synthetic neighbors for the query.
func (m *Mock) FindNeighbors(_ context.Context, indexEndpoint, _ string, _ []float64, count int) ([]driver.Neighbor, error) {
	if !m.indexEndpoints.Has(indexEndpoint) {
		return nil, errors.Newf(errors.NotFound, "index endpoint %q not found", indexEndpoint)
	}

	if count < 0 {
		count = 0
	}

	if count > maxNeighbors {
		count = maxNeighbors
	}

	out := make([]driver.Neighbor, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, driver.Neighbor{DatapointID: "dp-" + strconv.Itoa(i), Distance: float64(i) * 0.1})
	}

	return out, nil
}
