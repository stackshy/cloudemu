package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

func (m *Mock) CreateFeatureGroup(_ context.Context, cfg driver.FeatureGroupConfig) (*driver.Operation, *driver.FeatureGroup, error) {
	name := m.resName(cfg.Location, "featureGroups", orID(cfg.FeatureGroupID, m.newID()))
	fg := &driver.FeatureGroup{Name: name, Description: cfg.Description, BigQueryURI: cfg.BigQueryURI, CreateTime: m.now()}
	m.featureGroups.Set(name, fg)

	out := *fg

	return m.doneOp(cfg.Location, name), &out, nil
}

func orID(id, gen string) string {
	if id != "" {
		return id
	}

	return gen
}

func (m *Mock) GetFeatureGroup(_ context.Context, name string) (*driver.FeatureGroup, error) {
	fg, ok := m.featureGroups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "feature group %q not found", name)
	}

	out := *fg

	return &out, nil
}

func (m *Mock) ListFeatureGroups(_ context.Context, location string) ([]driver.FeatureGroup, error) {
	out := make([]driver.FeatureGroup, 0)

	for _, fg := range m.featureGroups.All() {
		if location == "" || locationOf(fg.Name) == location {
			out = append(out, *fg)
		}
	}

	return out, nil
}

func (m *Mock) DeleteFeatureGroup(_ context.Context, name string) (*driver.Operation, error) {
	if !m.featureGroups.Has(name) {
		return nil, errors.Newf(errors.NotFound, "feature group %q not found", name)
	}

	m.featureGroups.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

//nolint:dupl // parent/child create shape recurs; each maps a distinct resource.
func (m *Mock) CreateFeature(_ context.Context, parent, featureID, description string) (*driver.Operation, *driver.Feature, error) {
	if !m.featureGroups.Has(parent) {
		return nil, nil, errors.Newf(errors.NotFound, "feature group %q not found", parent)
	}

	name := parent + "/features/" + orID(featureID, m.newID())
	f := &driver.Feature{Name: name, Description: description, CreateTime: m.now()}
	m.features.Set(name, f)

	out := *f

	return m.doneOp(locationOf(parent), name), &out, nil
}

func (m *Mock) GetFeature(_ context.Context, name string) (*driver.Feature, error) {
	f, ok := m.features.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "feature %q not found", name)
	}

	out := *f

	return &out, nil
}

func (m *Mock) ListFeatures(_ context.Context, parent string) ([]driver.Feature, error) {
	out := make([]driver.Feature, 0)

	for k, f := range m.features.All() {
		if len(k) > len(parent) && k[:len(parent)] == parent {
			out = append(out, *f)
		}
	}

	return out, nil
}

func (m *Mock) DeleteFeature(_ context.Context, name string) (*driver.Operation, error) {
	if !m.features.Has(name) {
		return nil, errors.Newf(errors.NotFound, "feature %q not found", name)
	}

	m.features.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) CreateFeatureOnlineStore(
	_ context.Context, cfg driver.FeatureOnlineStoreConfig,
) (*driver.Operation, *driver.FeatureOnlineStore, error) {
	name := m.resName(cfg.Location, "featureOnlineStores", orID(cfg.FeatureOnlineStoreID, m.newID()))
	s := &driver.FeatureOnlineStore{Name: name, State: "STABLE", CreateTime: m.now()}
	m.onlineStores.Set(name, s)

	out := *s

	return m.doneOp(cfg.Location, name), &out, nil
}

func (m *Mock) GetFeatureOnlineStore(_ context.Context, name string) (*driver.FeatureOnlineStore, error) {
	s, ok := m.onlineStores.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "feature online store %q not found", name)
	}

	out := *s

	return &out, nil
}

func (m *Mock) ListFeatureOnlineStores(_ context.Context, location string) ([]driver.FeatureOnlineStore, error) {
	out := make([]driver.FeatureOnlineStore, 0)

	for _, s := range m.onlineStores.All() {
		if location == "" || locationOf(s.Name) == location {
			out = append(out, *s)
		}
	}

	return out, nil
}

func (m *Mock) DeleteFeatureOnlineStore(_ context.Context, name string) (*driver.Operation, error) {
	if !m.onlineStores.Has(name) {
		return nil, errors.Newf(errors.NotFound, "feature online store %q not found", name)
	}

	m.onlineStores.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) CreateFeatureView(_ context.Context, cfg driver.FeatureViewConfig) (*driver.Operation, *driver.FeatureView, error) {
	if !m.onlineStores.Has(cfg.Parent) {
		return nil, nil, errors.Newf(errors.NotFound, "feature online store %q not found", cfg.Parent)
	}

	name := cfg.Parent + "/featureViews/" + orID(cfg.FeatureViewID, m.newID())
	fv := &driver.FeatureView{Name: name, BigQueryURI: cfg.BigQueryURI, CreateTime: m.now()}
	m.featureViews.Set(name, fv)

	out := *fv

	return m.doneOp(locationOf(cfg.Parent), name), &out, nil
}

func (m *Mock) GetFeatureView(_ context.Context, name string) (*driver.FeatureView, error) {
	fv, ok := m.featureViews.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "feature view %q not found", name)
	}

	out := *fv

	return &out, nil
}

func (m *Mock) ListFeatureViews(_ context.Context, parent string) ([]driver.FeatureView, error) {
	out := make([]driver.FeatureView, 0)

	for k, fv := range m.featureViews.All() {
		if len(k) > len(parent) && k[:len(parent)] == parent {
			out = append(out, *fv)
		}
	}

	return out, nil
}

func (m *Mock) DeleteFeatureView(_ context.Context, name string) (*driver.Operation, error) {
	if !m.featureViews.Has(name) {
		return nil, errors.Newf(errors.NotFound, "feature view %q not found", name)
	}

	m.featureViews.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// FetchFeatureValues returns a deterministic synthetic value for the entity.
func (m *Mock) FetchFeatureValues(_ context.Context, featureView, entityID string) ([]driver.FeatureNameValue, error) {
	if !m.featureViews.Has(featureView) {
		return nil, errors.Newf(errors.NotFound, "feature view %q not found", featureView)
	}

	return []driver.FeatureNameValue{{Name: "entity_id", Value: entityID}}, nil
}

// --- Classic Featurestore / EntityType (pre-FeatureGroup) ---

func (m *Mock) CreateFeaturestore(_ context.Context, cfg driver.FeaturestoreConfig) (*driver.Operation, *driver.Featurestore, error) {
	name := m.resName(cfg.Location, "featurestores", orID(cfg.FeaturestoreID, m.newID()))
	fs := &driver.Featurestore{Name: name, State: "STABLE", OnlineNodeCount: cfg.OnlineNodeCount, CreateTime: m.now()}
	m.featurestores.Set(name, fs)

	out := *fs

	return m.doneOp(cfg.Location, name), &out, nil
}

func (m *Mock) GetFeaturestore(_ context.Context, name string) (*driver.Featurestore, error) {
	fs, ok := m.featurestores.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "featurestore %q not found", name)
	}

	out := *fs

	return &out, nil
}

func (m *Mock) ListFeaturestores(_ context.Context, location string) ([]driver.Featurestore, error) {
	out := make([]driver.Featurestore, 0)

	for _, fs := range m.featurestores.All() {
		if location == "" || locationOf(fs.Name) == location {
			out = append(out, *fs)
		}
	}

	return out, nil
}

func (m *Mock) DeleteFeaturestore(_ context.Context, name string) (*driver.Operation, error) {
	if !m.featurestores.Has(name) {
		return nil, errors.Newf(errors.NotFound, "featurestore %q not found", name)
	}

	m.featurestores.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

//nolint:dupl // parent/child create shape recurs; each maps a distinct resource.
func (m *Mock) CreateEntityType(
	_ context.Context, parent, entityTypeID, description string,
) (*driver.Operation, *driver.EntityType, error) {
	if !m.featurestores.Has(parent) {
		return nil, nil, errors.Newf(errors.NotFound, "featurestore %q not found", parent)
	}

	name := parent + "/entityTypes/" + orID(entityTypeID, m.newID())
	et := &driver.EntityType{Name: name, Description: description, CreateTime: m.now()}
	m.entityTypes.Set(name, et)

	out := *et

	return m.doneOp(locationOf(parent), name), &out, nil
}

func (m *Mock) GetEntityType(_ context.Context, name string) (*driver.EntityType, error) {
	et, ok := m.entityTypes.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "entity type %q not found", name)
	}

	out := *et

	return &out, nil
}

func (m *Mock) ListEntityTypes(_ context.Context, parent string) ([]driver.EntityType, error) {
	out := make([]driver.EntityType, 0)

	for k, et := range m.entityTypes.All() {
		if len(k) > len(parent) && k[:len(parent)] == parent {
			out = append(out, *et)
		}
	}

	return out, nil
}

func (m *Mock) DeleteEntityType(_ context.Context, name string) (*driver.Operation, error) {
	if !m.entityTypes.Has(name) {
		return nil, errors.Newf(errors.NotFound, "entity type %q not found", name)
	}

	m.entityTypes.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

func entityKey(entityType, entityID string) string {
	return entityType + "\x00" + entityID
}

// WriteFeatureValues stores online feature values for an entity.
func (m *Mock) WriteFeatureValues(_ context.Context, entityType, entityID string, values []driver.FeatureNameValue) error {
	if !m.entityTypes.Has(entityType) {
		return errors.Newf(errors.NotFound, "entity type %q not found", entityType)
	}

	stored := make([]driver.FeatureNameValue, len(values))
	copy(stored, values)
	m.entityRecords.Set(entityKey(entityType, entityID), stored)

	return nil
}

// ReadFeatureValues returns the online feature values for an entity.
func (m *Mock) ReadFeatureValues(_ context.Context, entityType, entityID string) ([]driver.FeatureNameValue, error) {
	if !m.entityTypes.Has(entityType) {
		return nil, errors.Newf(errors.NotFound, "entity type %q not found", entityType)
	}

	rec, ok := m.entityRecords.Get(entityKey(entityType, entityID))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "entity %q not found", entityID)
	}

	out := make([]driver.FeatureNameValue, len(rec))
	copy(out, rec)

	return out, nil
}
