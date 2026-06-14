package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func (v *VertexAI) CreateCustomJob(ctx context.Context, cfg driver.CustomJobConfig) (*driver.CustomJob, error) {
	return cast[*driver.CustomJob](v.do(ctx, "CreateCustomJob", cfg, func() (any, error) { return v.drv.CreateCustomJob(ctx, cfg) }))
}

func (v *VertexAI) GetCustomJob(ctx context.Context, name string) (*driver.CustomJob, error) {
	return cast[*driver.CustomJob](v.do(ctx, "GetCustomJob", name, func() (any, error) { return v.drv.GetCustomJob(ctx, name) }))
}

func (v *VertexAI) ListCustomJobs(ctx context.Context, location string) ([]driver.CustomJob, error) {
	return cast[[]driver.CustomJob](v.do(ctx, "ListCustomJobs", location, func() (any, error) { return v.drv.ListCustomJobs(ctx, location) }))
}

func (v *VertexAI) CancelCustomJob(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelCustomJob", name, func() (any, error) { return nil, v.drv.CancelCustomJob(ctx, name) })

	return err
}

func (v *VertexAI) DeleteCustomJob(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteCustomJob", name, func() (any, error) { return v.drv.DeleteCustomJob(ctx, name) }))
}

func (v *VertexAI) CreateHyperparameterTuningJob(
	ctx context.Context, cfg driver.HyperparameterTuningJobConfig,
) (*driver.HyperparameterTuningJob, error) {
	return cast[*driver.HyperparameterTuningJob](v.do(ctx, "CreateHyperparameterTuningJob", cfg, func() (any, error) {
		return v.drv.CreateHyperparameterTuningJob(ctx, cfg)
	}))
}

func (v *VertexAI) GetHyperparameterTuningJob(ctx context.Context, name string) (*driver.HyperparameterTuningJob, error) {
	return cast[*driver.HyperparameterTuningJob](v.do(ctx, "GetHyperparameterTuningJob", name, func() (any, error) {
		return v.drv.GetHyperparameterTuningJob(ctx, name)
	}))
}

func (v *VertexAI) ListHyperparameterTuningJobs(ctx context.Context, location string) ([]driver.HyperparameterTuningJob, error) {
	return cast[[]driver.HyperparameterTuningJob](v.do(ctx, "ListHyperparameterTuningJobs", location, func() (any, error) {
		return v.drv.ListHyperparameterTuningJobs(ctx, location)
	}))
}

func (v *VertexAI) CancelHyperparameterTuningJob(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelHyperparameterTuningJob", name, func() (any, error) {
		return nil, v.drv.CancelHyperparameterTuningJob(ctx, name)
	})

	return err
}

//nolint:gocritic // cfg matches the driver signature; forwarded unchanged.
func (v *VertexAI) CreateBatchPredictionJob(
	ctx context.Context, cfg driver.BatchPredictionJobConfig,
) (*driver.BatchPredictionJob, error) {
	return cast[*driver.BatchPredictionJob](v.do(ctx, "CreateBatchPredictionJob", cfg, func() (any, error) {
		return v.drv.CreateBatchPredictionJob(ctx, cfg)
	}))
}

func (v *VertexAI) GetBatchPredictionJob(ctx context.Context, name string) (*driver.BatchPredictionJob, error) {
	return cast[*driver.BatchPredictionJob](v.do(ctx, "GetBatchPredictionJob", name, func() (any, error) {
		return v.drv.GetBatchPredictionJob(ctx, name)
	}))
}

func (v *VertexAI) ListBatchPredictionJobs(ctx context.Context, location string) ([]driver.BatchPredictionJob, error) {
	return cast[[]driver.BatchPredictionJob](v.do(ctx, "ListBatchPredictionJobs", location, func() (any, error) {
		return v.drv.ListBatchPredictionJobs(ctx, location)
	}))
}

func (v *VertexAI) CancelBatchPredictionJob(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelBatchPredictionJob", name, func() (any, error) {
		return nil, v.drv.CancelBatchPredictionJob(ctx, name)
	})

	return err
}

func (v *VertexAI) DeleteBatchPredictionJob(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteBatchPredictionJob", name, func() (any, error) {
		return v.drv.DeleteBatchPredictionJob(ctx, name)
	}))
}
