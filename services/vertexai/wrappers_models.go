package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (v *VertexAI) UploadModel(ctx context.Context, cfg driver.ModelConfig) (*driver.Operation, *driver.Model, error) {
	r, err := cast[opPair[*driver.Model]](v.do(ctx, "UploadModel", cfg, func() (any, error) {
		op, m, e := v.drv.UploadModel(ctx, cfg)

		return opPair[*driver.Model]{op, m}, e
	}))

	return r.op, r.res, err
}

func (v *VertexAI) GetModel(ctx context.Context, name string) (*driver.Model, error) {
	return cast[*driver.Model](v.do(ctx, "GetModel", name, func() (any, error) { return v.drv.GetModel(ctx, name) }))
}

func (v *VertexAI) ListModels(ctx context.Context, location string) ([]driver.Model, error) {
	return cast[[]driver.Model](v.do(ctx, "ListModels", location, func() (any, error) { return v.drv.ListModels(ctx, location) }))
}

func (v *VertexAI) PatchModel(ctx context.Context, name, displayName, description string) (*driver.Model, error) {
	return cast[*driver.Model](v.do(ctx, "PatchModel", name, func() (any, error) {
		return v.drv.PatchModel(ctx, name, displayName, description)
	}))
}

func (v *VertexAI) DeleteModel(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteModel", name, func() (any, error) { return v.drv.DeleteModel(ctx, name) }))
}

func (v *VertexAI) ListModelVersions(ctx context.Context, name string) ([]driver.Model, error) {
	return cast[[]driver.Model](v.do(ctx, "ListModelVersions", name, func() (any, error) { return v.drv.ListModelVersions(ctx, name) }))
}

func (v *VertexAI) DeleteModelVersion(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteModelVersion", name, func() (any, error) {
		return v.drv.DeleteModelVersion(ctx, name)
	}))
}

func (v *VertexAI) ImportModelEvaluation(
	ctx context.Context, modelName string, eval driver.ModelEvaluation,
) (*driver.ModelEvaluation, error) {
	return cast[*driver.ModelEvaluation](v.do(ctx, "ImportModelEvaluation", modelName, func() (any, error) {
		return v.drv.ImportModelEvaluation(ctx, modelName, eval)
	}))
}

func (v *VertexAI) GetModelEvaluation(ctx context.Context, name string) (*driver.ModelEvaluation, error) {
	return cast[*driver.ModelEvaluation](v.do(ctx, "GetModelEvaluation", name, func() (any, error) {
		return v.drv.GetModelEvaluation(ctx, name)
	}))
}

func (v *VertexAI) ListModelEvaluations(ctx context.Context, modelName string) ([]driver.ModelEvaluation, error) {
	return cast[[]driver.ModelEvaluation](v.do(ctx, "ListModelEvaluations", modelName, func() (any, error) {
		return v.drv.ListModelEvaluations(ctx, modelName)
	}))
}
