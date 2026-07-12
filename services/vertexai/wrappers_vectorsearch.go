package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func (v *VertexAI) CreateIndex(ctx context.Context, cfg driver.IndexConfig) (*driver.Operation, *driver.Index, error) {
	r, err := cast[opPair[*driver.Index]](v.do(ctx, "CreateIndex", cfg, func() (any, error) {
		op, idx, e := v.drv.CreateIndex(ctx, cfg)

		return opPair[*driver.Index]{op, idx}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetIndex(ctx context.Context, name string) (*driver.Index, error) {
	return cast[*driver.Index](v.do(ctx, "GetIndex", name, func() (any, error) { return v.drv.GetIndex(ctx, name) }))
}

func (v *VertexAI) ListIndexes(ctx context.Context, location string) ([]driver.Index, error) {
	return cast[[]driver.Index](v.do(ctx, "ListIndexes", location, func() (any, error) { return v.drv.ListIndexes(ctx, location) }))
}

func (v *VertexAI) DeleteIndex(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteIndex", name, func() (any, error) { return v.drv.DeleteIndex(ctx, name) }))
}

func (v *VertexAI) UpsertDatapoints(ctx context.Context, index string, datapoints []driver.Datapoint) error {
	_, err := v.do(ctx, "UpsertDatapoints", index, func() (any, error) {
		return nil, v.drv.UpsertDatapoints(ctx, index, datapoints)
	})

	return err
}

func (v *VertexAI) RemoveDatapoints(ctx context.Context, index string, datapointIDs []string) error {
	_, err := v.do(ctx, "RemoveDatapoints", index, func() (any, error) {
		return nil, v.drv.RemoveDatapoints(ctx, index, datapointIDs)
	})

	return err
}

func (v *VertexAI) CreateIndexEndpoint(
	ctx context.Context, cfg driver.IndexEndpointConfig,
) (*driver.Operation, *driver.IndexEndpoint, error) {
	r, err := cast[opPair[*driver.IndexEndpoint]](v.do(ctx, "CreateIndexEndpoint", cfg, func() (any, error) {
		op, ie, e := v.drv.CreateIndexEndpoint(ctx, cfg)

		return opPair[*driver.IndexEndpoint]{op, ie}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetIndexEndpoint(ctx context.Context, name string) (*driver.IndexEndpoint, error) {
	return cast[*driver.IndexEndpoint](v.do(ctx, "GetIndexEndpoint", name, func() (any, error) {
		return v.drv.GetIndexEndpoint(ctx, name)
	}))
}

func (v *VertexAI) ListIndexEndpoints(ctx context.Context, location string) ([]driver.IndexEndpoint, error) {
	return cast[[]driver.IndexEndpoint](v.do(ctx, "ListIndexEndpoints", location, func() (any, error) {
		return v.drv.ListIndexEndpoints(ctx, location)
	}))
}

func (v *VertexAI) DeleteIndexEndpoint(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteIndexEndpoint", name, func() (any, error) {
		return v.drv.DeleteIndexEndpoint(ctx, name)
	}))
}

func (v *VertexAI) DeployIndex(
	ctx context.Context, indexEndpoint string, di driver.DeployedIndex,
) (*driver.Operation, *driver.IndexEndpoint, error) {
	r, err := cast[opPair[*driver.IndexEndpoint]](v.do(ctx, "DeployIndex", indexEndpoint, func() (any, error) {
		op, ie, e := v.drv.DeployIndex(ctx, indexEndpoint, di)

		return opPair[*driver.IndexEndpoint]{op, ie}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) UndeployIndex(
	ctx context.Context, indexEndpoint, deployedIndexID string,
) (*driver.Operation, *driver.IndexEndpoint, error) {
	r, err := cast[opPair[*driver.IndexEndpoint]](v.do(ctx, "UndeployIndex", indexEndpoint, func() (any, error) {
		op, ie, e := v.drv.UndeployIndex(ctx, indexEndpoint, deployedIndexID)

		return opPair[*driver.IndexEndpoint]{op, ie}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) FindNeighbors(
	ctx context.Context, indexEndpoint, deployedIndexID string, query []float64, count int,
) ([]driver.Neighbor, error) {
	return cast[[]driver.Neighbor](v.do(ctx, "FindNeighbors", indexEndpoint, func() (any, error) {
		return v.drv.FindNeighbors(ctx, indexEndpoint, deployedIndexID, query, count)
	}))
}
