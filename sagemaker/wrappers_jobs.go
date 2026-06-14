package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/sagemaker/driver"
)

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateTrainingJob(ctx context.Context, cfg driver.TrainingJobConfig) (*driver.TrainingJob, error) {
	return cast[*driver.TrainingJob](s.do(ctx, "CreateTrainingJob", cfg, func() (any, error) { return s.drv.CreateTrainingJob(ctx, cfg) }))
}

func (s *SageMaker) DescribeTrainingJob(ctx context.Context, name string) (*driver.TrainingJob, error) {
	return cast[*driver.TrainingJob](s.do(ctx, "DescribeTrainingJob", name, func() (any, error) {
		return s.drv.DescribeTrainingJob(ctx, name)
	}))
}

func (s *SageMaker) ListTrainingJobs(ctx context.Context) ([]driver.TrainingJob, error) {
	return cast[[]driver.TrainingJob](s.do(ctx, "ListTrainingJobs", nil, func() (any, error) { return s.drv.ListTrainingJobs(ctx) }))
}

func (s *SageMaker) StopTrainingJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopTrainingJob", name, func() (any, error) { return nil, s.drv.StopTrainingJob(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateProcessingJob(ctx context.Context, cfg driver.ProcessingJobConfig) (*driver.ProcessingJob, error) {
	return cast[*driver.ProcessingJob](s.do(ctx, "CreateProcessingJob", cfg, func() (any, error) {
		return s.drv.CreateProcessingJob(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeProcessingJob(ctx context.Context, name string) (*driver.ProcessingJob, error) {
	return cast[*driver.ProcessingJob](s.do(ctx, "DescribeProcessingJob", name, func() (any, error) {
		return s.drv.DescribeProcessingJob(ctx, name)
	}))
}

func (s *SageMaker) ListProcessingJobs(ctx context.Context) ([]driver.ProcessingJob, error) {
	return cast[[]driver.ProcessingJob](s.do(ctx, "ListProcessingJobs", nil, func() (any, error) { return s.drv.ListProcessingJobs(ctx) }))
}

func (s *SageMaker) StopProcessingJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopProcessingJob", name, func() (any, error) { return nil, s.drv.StopProcessingJob(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateTransformJob(ctx context.Context, cfg driver.TransformJobConfig) (*driver.TransformJob, error) {
	return cast[*driver.TransformJob](s.do(ctx, "CreateTransformJob", cfg, func() (any, error) { return s.drv.CreateTransformJob(ctx, cfg) }))
}

func (s *SageMaker) DescribeTransformJob(ctx context.Context, name string) (*driver.TransformJob, error) {
	return cast[*driver.TransformJob](s.do(ctx, "DescribeTransformJob", name, func() (any, error) {
		return s.drv.DescribeTransformJob(ctx, name)
	}))
}

func (s *SageMaker) ListTransformJobs(ctx context.Context) ([]driver.TransformJob, error) {
	return cast[[]driver.TransformJob](s.do(ctx, "ListTransformJobs", nil, func() (any, error) { return s.drv.ListTransformJobs(ctx) }))
}

func (s *SageMaker) StopTransformJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopTransformJob", name, func() (any, error) { return nil, s.drv.StopTransformJob(ctx, name) })

	return err
}

func (s *SageMaker) CreateHyperParameterTuningJob(
	//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
	ctx context.Context, cfg driver.HyperParameterTuningJobConfig,
) (*driver.HyperParameterTuningJob, error) {
	return cast[*driver.HyperParameterTuningJob](s.do(ctx, "CreateHyperParameterTuningJob", cfg, func() (any, error) {
		return s.drv.CreateHyperParameterTuningJob(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeHyperParameterTuningJob(ctx context.Context, name string) (*driver.HyperParameterTuningJob, error) {
	return cast[*driver.HyperParameterTuningJob](s.do(ctx, "DescribeHyperParameterTuningJob", name, func() (any, error) {
		return s.drv.DescribeHyperParameterTuningJob(ctx, name)
	}))
}

func (s *SageMaker) ListHyperParameterTuningJobs(ctx context.Context) ([]driver.HyperParameterTuningJob, error) {
	return cast[[]driver.HyperParameterTuningJob](s.do(ctx, "ListHyperParameterTuningJobs", nil, func() (any, error) {
		return s.drv.ListHyperParameterTuningJobs(ctx)
	}))
}

func (s *SageMaker) StopHyperParameterTuningJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopHyperParameterTuningJob", name, func() (any, error) { return nil, s.drv.StopHyperParameterTuningJob(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateAutoMLJobV2(ctx context.Context, cfg driver.AutoMLJobConfig) (*driver.AutoMLJob, error) {
	return cast[*driver.AutoMLJob](s.do(ctx, "CreateAutoMLJobV2", cfg, func() (any, error) { return s.drv.CreateAutoMLJobV2(ctx, cfg) }))
}

func (s *SageMaker) DescribeAutoMLJobV2(ctx context.Context, name string) (*driver.AutoMLJob, error) {
	return cast[*driver.AutoMLJob](s.do(ctx, "DescribeAutoMLJobV2", name, func() (any, error) { return s.drv.DescribeAutoMLJobV2(ctx, name) }))
}

func (s *SageMaker) ListAutoMLJobs(ctx context.Context) ([]driver.AutoMLJob, error) {
	return cast[[]driver.AutoMLJob](s.do(ctx, "ListAutoMLJobs", nil, func() (any, error) { return s.drv.ListAutoMLJobs(ctx) }))
}

func (s *SageMaker) StopAutoMLJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopAutoMLJob", name, func() (any, error) { return nil, s.drv.StopAutoMLJob(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateLabelingJob(ctx context.Context, cfg driver.LabelingJobConfig) (*driver.LabelingJob, error) {
	return cast[*driver.LabelingJob](s.do(ctx, "CreateLabelingJob", cfg, func() (any, error) { return s.drv.CreateLabelingJob(ctx, cfg) }))
}

func (s *SageMaker) DescribeLabelingJob(ctx context.Context, name string) (*driver.LabelingJob, error) {
	return cast[*driver.LabelingJob](s.do(ctx, "DescribeLabelingJob", name, func() (any, error) {
		return s.drv.DescribeLabelingJob(ctx, name)
	}))
}

func (s *SageMaker) ListLabelingJobs(ctx context.Context) ([]driver.LabelingJob, error) {
	return cast[[]driver.LabelingJob](s.do(ctx, "ListLabelingJobs", nil, func() (any, error) { return s.drv.ListLabelingJobs(ctx) }))
}

func (s *SageMaker) StopLabelingJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopLabelingJob", name, func() (any, error) { return nil, s.drv.StopLabelingJob(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateCompilationJob(ctx context.Context, cfg driver.CompilationJobConfig) (*driver.CompilationJob, error) {
	return cast[*driver.CompilationJob](s.do(ctx, "CreateCompilationJob", cfg, func() (any, error) {
		return s.drv.CreateCompilationJob(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeCompilationJob(ctx context.Context, name string) (*driver.CompilationJob, error) {
	return cast[*driver.CompilationJob](s.do(ctx, "DescribeCompilationJob", name, func() (any, error) {
		return s.drv.DescribeCompilationJob(ctx, name)
	}))
}

func (s *SageMaker) ListCompilationJobs(ctx context.Context) ([]driver.CompilationJob, error) {
	return cast[[]driver.CompilationJob](s.do(ctx, "ListCompilationJobs", nil, func() (any, error) { return s.drv.ListCompilationJobs(ctx) }))
}

func (s *SageMaker) StopCompilationJob(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopCompilationJob", name, func() (any, error) { return nil, s.drv.StopCompilationJob(ctx, name) })

	return err
}
