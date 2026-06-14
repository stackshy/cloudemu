package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func (v *VertexAI) CreateTrainingPipeline(
	ctx context.Context, cfg driver.TrainingPipelineConfig,
) (*driver.TrainingPipeline, error) {
	return cast[*driver.TrainingPipeline](v.do(ctx, "CreateTrainingPipeline", cfg, func() (any, error) {
		return v.drv.CreateTrainingPipeline(ctx, cfg)
	}))
}

func (v *VertexAI) GetTrainingPipeline(ctx context.Context, name string) (*driver.TrainingPipeline, error) {
	return cast[*driver.TrainingPipeline](v.do(ctx, "GetTrainingPipeline", name, func() (any, error) {
		return v.drv.GetTrainingPipeline(ctx, name)
	}))
}

func (v *VertexAI) ListTrainingPipelines(ctx context.Context, location string) ([]driver.TrainingPipeline, error) {
	return cast[[]driver.TrainingPipeline](v.do(ctx, "ListTrainingPipelines", location, func() (any, error) {
		return v.drv.ListTrainingPipelines(ctx, location)
	}))
}

func (v *VertexAI) CancelTrainingPipeline(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelTrainingPipeline", name, func() (any, error) {
		return nil, v.drv.CancelTrainingPipeline(ctx, name)
	})

	return err
}

func (v *VertexAI) DeleteTrainingPipeline(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeleteTrainingPipeline", name, func() (any, error) {
		return v.drv.DeleteTrainingPipeline(ctx, name)
	}))
}

func (v *VertexAI) CreatePipelineJob(ctx context.Context, cfg driver.PipelineJobConfig) (*driver.PipelineJob, error) {
	return cast[*driver.PipelineJob](v.do(ctx, "CreatePipelineJob", cfg, func() (any, error) {
		return v.drv.CreatePipelineJob(ctx, cfg)
	}))
}

func (v *VertexAI) GetPipelineJob(ctx context.Context, name string) (*driver.PipelineJob, error) {
	return cast[*driver.PipelineJob](v.do(ctx, "GetPipelineJob", name, func() (any, error) { return v.drv.GetPipelineJob(ctx, name) }))
}

func (v *VertexAI) ListPipelineJobs(ctx context.Context, location string) ([]driver.PipelineJob, error) {
	return cast[[]driver.PipelineJob](v.do(ctx, "ListPipelineJobs", location, func() (any, error) {
		return v.drv.ListPipelineJobs(ctx, location)
	}))
}

func (v *VertexAI) CancelPipelineJob(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelPipelineJob", name, func() (any, error) { return nil, v.drv.CancelPipelineJob(ctx, name) })

	return err
}

func (v *VertexAI) DeletePipelineJob(ctx context.Context, name string) (*driver.Operation, error) {
	return cast[*driver.Operation](v.do(ctx, "DeletePipelineJob", name, func() (any, error) {
		return v.drv.DeletePipelineJob(ctx, name)
	}))
}

func (v *VertexAI) CreateTuningJob(ctx context.Context, cfg driver.TuningJobConfig) (*driver.TuningJob, error) {
	return cast[*driver.TuningJob](v.do(ctx, "CreateTuningJob", cfg, func() (any, error) { return v.drv.CreateTuningJob(ctx, cfg) }))
}

func (v *VertexAI) GetTuningJob(ctx context.Context, name string) (*driver.TuningJob, error) {
	return cast[*driver.TuningJob](v.do(ctx, "GetTuningJob", name, func() (any, error) { return v.drv.GetTuningJob(ctx, name) }))
}

func (v *VertexAI) ListTuningJobs(ctx context.Context, location string) ([]driver.TuningJob, error) {
	return cast[[]driver.TuningJob](v.do(ctx, "ListTuningJobs", location, func() (any, error) { return v.drv.ListTuningJobs(ctx, location) }))
}

func (v *VertexAI) CancelTuningJob(ctx context.Context, name string) error {
	_, err := v.do(ctx, "CancelTuningJob", name, func() (any, error) { return nil, v.drv.CancelTuningJob(ctx, name) })

	return err
}
