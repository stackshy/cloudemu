package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func (v *VertexAI) CreateDataset(ctx context.Context, cfg driver.DatasetConfig) (*driver.Operation, *driver.Dataset, error) {
	r, err := cast[opPair[*driver.Dataset]](v.do(ctx, "CreateDataset", cfg, func() (any, error) {
		op, ds, e := v.drv.CreateDataset(ctx, cfg)

		return opPair[*driver.Dataset]{op, ds}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetDataset(ctx context.Context, name string) (*driver.Dataset, error) {
	return cast[*driver.Dataset](v.do(ctx, "GetDataset", name, func() (any, error) { return v.drv.GetDataset(ctx, name) }))
}

func (v *VertexAI) ListDatasets(ctx context.Context, location string) ([]driver.Dataset, error) {
	return cast[[]driver.Dataset](v.do(ctx, "ListDatasets", location, func() (any, error) { return v.drv.ListDatasets(ctx, location) }))
}

func (v *VertexAI) PatchDataset(ctx context.Context, name, displayName string) (*driver.Dataset, error) {
	return cast[*driver.Dataset](v.do(ctx, "PatchDataset", name, func() (any, error) {
		return v.drv.PatchDataset(ctx, name, displayName)
	}))
}

func (v *VertexAI) DeleteDataset(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteDataset", name, func() (any, error) { return v.drv.DeleteDataset(ctx, name) }))
}

func (v *VertexAI) ImportData(ctx context.Context, name, gcsURI string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "ImportData", name, func() (any, error) { return v.drv.ImportData(ctx, name, gcsURI) }))
}

func (v *VertexAI) ExportData(ctx context.Context, name, gcsOutputURI string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "ExportData", name, func() (any, error) { return v.drv.ExportData(ctx, name, gcsOutputURI) }))
}
