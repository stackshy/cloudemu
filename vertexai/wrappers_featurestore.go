package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Legacy Featurestore / EntityType / Feature values ---

func (v *VertexAI) CreateFeaturestore(
	ctx context.Context, cfg driver.FeaturestoreConfig,
) (*driver.Operation, *driver.Featurestore, error) {
	r, err := cast[opPair[*driver.Featurestore]](v.do(ctx, "CreateFeaturestore", cfg, func() (any, error) {
		op, fs, e := v.drv.CreateFeaturestore(ctx, cfg)

		return opPair[*driver.Featurestore]{op, fs}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetFeaturestore(ctx context.Context, name string) (*driver.Featurestore, error) {
	return cast[*driver.Featurestore](v.do(ctx, "GetFeaturestore", name, func() (any, error) { return v.drv.GetFeaturestore(ctx, name) }))
}

func (v *VertexAI) ListFeaturestores(ctx context.Context, location string) ([]driver.Featurestore, error) {
	return cast[[]driver.Featurestore](v.do(ctx, "ListFeaturestores", location, func() (any, error) {
		return v.drv.ListFeaturestores(ctx, location)
	}))
}

func (v *VertexAI) DeleteFeaturestore(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteFeaturestore", name, func() (any, error) {
		return v.drv.DeleteFeaturestore(ctx, name)
	}))
}

func (v *VertexAI) CreateEntityType(
	ctx context.Context, parent, entityTypeID, description string,
) (*driver.Operation, *driver.EntityType, error) {
	r, err := cast[opPair[*driver.EntityType]](v.do(ctx, "CreateEntityType", parent, func() (any, error) {
		op, et, e := v.drv.CreateEntityType(ctx, parent, entityTypeID, description)

		return opPair[*driver.EntityType]{op, et}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetEntityType(ctx context.Context, name string) (*driver.EntityType, error) {
	return cast[*driver.EntityType](v.do(ctx, "GetEntityType", name, func() (any, error) { return v.drv.GetEntityType(ctx, name) }))
}

func (v *VertexAI) ListEntityTypes(ctx context.Context, parent string) ([]driver.EntityType, error) {
	return cast[[]driver.EntityType](v.do(ctx, "ListEntityTypes", parent, func() (any, error) { return v.drv.ListEntityTypes(ctx, parent) }))
}

func (v *VertexAI) DeleteEntityType(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteEntityType", name, func() (any, error) {
		return v.drv.DeleteEntityType(ctx, name)
	}))
}

func (v *VertexAI) WriteFeatureValues(
	ctx context.Context, entityType, entityID string, values []driver.FeatureNameValue,
) error {
	_, err := v.do(ctx, "WriteFeatureValues", entityType, func() (any, error) {
		return nil, v.drv.WriteFeatureValues(ctx, entityType, entityID, values)
	})

	return err
}

func (v *VertexAI) ReadFeatureValues(
	ctx context.Context, entityType, entityID string,
) ([]driver.FeatureNameValue, error) {
	return cast[[]driver.FeatureNameValue](v.do(ctx, "ReadFeatureValues", entityType, func() (any, error) {
		return v.drv.ReadFeatureValues(ctx, entityType, entityID)
	}))
}

func (v *VertexAI) CreateFeature(
	ctx context.Context, parent, featureID, description string,
) (*driver.Operation, *driver.Feature, error) {
	r, err := cast[opPair[*driver.Feature]](v.do(ctx, "CreateFeature", parent, func() (any, error) {
		op, f, e := v.drv.CreateFeature(ctx, parent, featureID, description)

		return opPair[*driver.Feature]{op, f}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetFeature(ctx context.Context, name string) (*driver.Feature, error) {
	return cast[*driver.Feature](v.do(ctx, "GetFeature", name, func() (any, error) { return v.drv.GetFeature(ctx, name) }))
}

func (v *VertexAI) ListFeatures(ctx context.Context, parent string) ([]driver.Feature, error) {
	return cast[[]driver.Feature](v.do(ctx, "ListFeatures", parent, func() (any, error) { return v.drv.ListFeatures(ctx, parent) }))
}

func (v *VertexAI) DeleteFeature(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteFeature", name, func() (any, error) { return v.drv.DeleteFeature(ctx, name) }))
}

// --- Feature Registry (featureGroups) ---

func (v *VertexAI) CreateFeatureGroup(
	ctx context.Context, cfg driver.FeatureGroupConfig,
) (*driver.Operation, *driver.FeatureGroup, error) {
	r, err := cast[opPair[*driver.FeatureGroup]](v.do(ctx, "CreateFeatureGroup", cfg, func() (any, error) {
		op, fg, e := v.drv.CreateFeatureGroup(ctx, cfg)

		return opPair[*driver.FeatureGroup]{op, fg}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetFeatureGroup(ctx context.Context, name string) (*driver.FeatureGroup, error) {
	return cast[*driver.FeatureGroup](v.do(ctx, "GetFeatureGroup", name, func() (any, error) { return v.drv.GetFeatureGroup(ctx, name) }))
}

func (v *VertexAI) ListFeatureGroups(ctx context.Context, location string) ([]driver.FeatureGroup, error) {
	return cast[[]driver.FeatureGroup](v.do(ctx, "ListFeatureGroups", location, func() (any, error) {
		return v.drv.ListFeatureGroups(ctx, location)
	}))
}

func (v *VertexAI) DeleteFeatureGroup(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteFeatureGroup", name, func() (any, error) {
		return v.drv.DeleteFeatureGroup(ctx, name)
	}))
}

// --- Feature Online Stores ---

func (v *VertexAI) CreateFeatureOnlineStore(
	ctx context.Context, cfg driver.FeatureOnlineStoreConfig,
) (*driver.Operation, *driver.FeatureOnlineStore, error) {
	r, err := cast[opPair[*driver.FeatureOnlineStore]](v.do(ctx, "CreateFeatureOnlineStore", cfg, func() (any, error) {
		op, fos, e := v.drv.CreateFeatureOnlineStore(ctx, cfg)

		return opPair[*driver.FeatureOnlineStore]{op, fos}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetFeatureOnlineStore(ctx context.Context, name string) (*driver.FeatureOnlineStore, error) {
	return cast[*driver.FeatureOnlineStore](v.do(ctx, "GetFeatureOnlineStore", name, func() (any, error) {
		return v.drv.GetFeatureOnlineStore(ctx, name)
	}))
}

func (v *VertexAI) ListFeatureOnlineStores(ctx context.Context, location string) ([]driver.FeatureOnlineStore, error) {
	return cast[[]driver.FeatureOnlineStore](v.do(ctx, "ListFeatureOnlineStores", location, func() (any, error) {
		return v.drv.ListFeatureOnlineStores(ctx, location)
	}))
}

func (v *VertexAI) DeleteFeatureOnlineStore(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteFeatureOnlineStore", name, func() (any, error) {
		return v.drv.DeleteFeatureOnlineStore(ctx, name)
	}))
}

func (v *VertexAI) CreateFeatureView(
	ctx context.Context, cfg driver.FeatureViewConfig,
) (*driver.Operation, *driver.FeatureView, error) {
	r, err := cast[opPair[*driver.FeatureView]](v.do(ctx, "CreateFeatureView", cfg, func() (any, error) {
		op, fv, e := v.drv.CreateFeatureView(ctx, cfg)

		return opPair[*driver.FeatureView]{op, fv}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetFeatureView(ctx context.Context, name string) (*driver.FeatureView, error) {
	return cast[*driver.FeatureView](v.do(ctx, "GetFeatureView", name, func() (any, error) { return v.drv.GetFeatureView(ctx, name) }))
}

func (v *VertexAI) ListFeatureViews(ctx context.Context, parent string) ([]driver.FeatureView, error) {
	return cast[[]driver.FeatureView](v.do(ctx, "ListFeatureViews", parent, func() (any, error) {
		return v.drv.ListFeatureViews(ctx, parent)
	}))
}

func (v *VertexAI) DeleteFeatureView(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteFeatureView", name, func() (any, error) {
		return v.drv.DeleteFeatureView(ctx, name)
	}))
}

func (v *VertexAI) FetchFeatureValues(
	ctx context.Context, featureView, entityID string,
) ([]driver.FeatureNameValue, error) {
	return cast[[]driver.FeatureNameValue](v.do(ctx, "FetchFeatureValues", featureView, func() (any, error) {
		return v.drv.FetchFeatureValues(ctx, featureView, entityID)
	}))
}
