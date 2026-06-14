package vertexai

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

// locationOf extracts the location segment from a resource name, or the
// default when absent.
func locationOf(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		if p == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	return defaultLocation
}

func (m *Mock) CreateDataset(_ context.Context, cfg driver.DatasetConfig) (*driver.Operation, *driver.Dataset, error) {
	now := m.now()
	name := m.resName(cfg.Location, "datasets", m.newID())
	ds := &driver.Dataset{
		Name:              name,
		DisplayName:       cfg.DisplayName,
		MetadataSchemaURI: cfg.MetadataSchemaURI,
		Labels:            copyLabels(cfg.Labels),
		CreateTime:        now,
		UpdateTime:        now,
	}
	m.datasets.Set(name, ds)
	m.emitMetric("dataset/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	return m.doneOp(cfg.Location, name), cloneDataset(ds), nil
}

func (m *Mock) GetDataset(_ context.Context, name string) (*driver.Dataset, error) {
	ds, ok := m.datasets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "dataset %q not found", name)
	}

	return cloneDataset(ds), nil
}

func (m *Mock) ListDatasets(_ context.Context, location string) ([]driver.Dataset, error) {
	out := make([]driver.Dataset, 0)

	for _, ds := range m.datasets.All() {
		if location == "" || locationOf(ds.Name) == location {
			out = append(out, *cloneDataset(ds))
		}
	}

	return out, nil
}

func (m *Mock) PatchDataset(_ context.Context, name, displayName string) (*driver.Dataset, error) {
	ds, ok := m.datasets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "dataset %q not found", name)
	}

	// Copy-then-Set: never mutate the stored pointer in place.
	updated := *ds
	if displayName != "" {
		updated.DisplayName = displayName
	}

	updated.UpdateTime = m.now()
	m.datasets.Set(name, &updated)

	return cloneDataset(&updated), nil
}

func (m *Mock) DeleteDataset(_ context.Context, name string) (*driver.Operation, error) {
	if !m.datasets.Has(name) {
		return nil, errors.Newf(errors.NotFound, "dataset %q not found", name)
	}

	m.datasets.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) ImportData(_ context.Context, name, _ string) (*driver.Operation, error) {
	if !m.datasets.Has(name) {
		return nil, errors.Newf(errors.NotFound, "dataset %q not found", name)
	}

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) ExportData(_ context.Context, name, _ string) (*driver.Operation, error) {
	if !m.datasets.Has(name) {
		return nil, errors.Newf(errors.NotFound, "dataset %q not found", name)
	}

	return m.doneOp(locationOf(name), name), nil
}
