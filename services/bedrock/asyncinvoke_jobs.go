package bedrock

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Async invocation ---

// StartAsyncInvoke starts an asynchronous model invocation.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) StartAsyncInvoke(ctx context.Context, cfg driver.StartAsyncInvokeConfig) (*driver.AsyncInvoke, error) {
	out, err := b.do(ctx, "StartAsyncInvoke", cfg.ModelID, func() (any, error) { return b.driver.StartAsyncInvoke(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.AsyncInvoke), nil
}

// GetAsyncInvoke retrieves an async invocation by its invocation ARN.
func (b *Bedrock) GetAsyncInvoke(ctx context.Context, invocationARN string) (*driver.AsyncInvoke, error) {
	out, err := b.do(ctx, "GetAsyncInvoke", invocationARN, func() (any, error) {
		return b.driver.GetAsyncInvoke(ctx, invocationARN)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.AsyncInvoke), nil
}

// ListAsyncInvokes lists all async invocations.
func (b *Bedrock) ListAsyncInvokes(ctx context.Context) ([]driver.AsyncInvoke, error) {
	out, err := b.do(ctx, "ListAsyncInvokes", nil, func() (any, error) { return b.driver.ListAsyncInvokes(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.AsyncInvoke), nil
}

// --- Model import jobs ---

// CreateModelImportJob starts a custom-model import job.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreateModelImportJob(ctx context.Context, cfg driver.ModelImportJobConfig) (*driver.ModelImportJob, error) {
	out, err := b.do(ctx, "CreateModelImportJob", cfg.JobName, func() (any, error) {
		return b.driver.CreateModelImportJob(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ModelImportJob), nil
}

// GetModelImportJob retrieves an import job by name or ARN.
func (b *Bedrock) GetModelImportJob(ctx context.Context, jobIdentifier string) (*driver.ModelImportJob, error) {
	out, err := b.do(ctx, "GetModelImportJob", jobIdentifier, func() (any, error) {
		return b.driver.GetModelImportJob(ctx, jobIdentifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ModelImportJob), nil
}

// ListModelImportJobs lists all import jobs.
func (b *Bedrock) ListModelImportJobs(ctx context.Context) ([]driver.ModelImportJob, error) {
	out, err := b.do(ctx, "ListModelImportJobs", nil, func() (any, error) { return b.driver.ListModelImportJobs(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ModelImportJob), nil
}

// --- Model copy jobs ---

// CreateModelCopyJob starts a model-copy job.
func (b *Bedrock) CreateModelCopyJob(ctx context.Context, cfg driver.ModelCopyJobConfig) (*driver.ModelCopyJob, error) {
	out, err := b.do(ctx, "CreateModelCopyJob", cfg.TargetModelName, func() (any, error) {
		return b.driver.CreateModelCopyJob(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ModelCopyJob), nil
}

// GetModelCopyJob retrieves a copy job by its job ARN.
func (b *Bedrock) GetModelCopyJob(ctx context.Context, jobARN string) (*driver.ModelCopyJob, error) {
	out, err := b.do(ctx, "GetModelCopyJob", jobARN, func() (any, error) { return b.driver.GetModelCopyJob(ctx, jobARN) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ModelCopyJob), nil
}

// ListModelCopyJobs lists all copy jobs.
func (b *Bedrock) ListModelCopyJobs(ctx context.Context) ([]driver.ModelCopyJob, error) {
	out, err := b.do(ctx, "ListModelCopyJobs", nil, func() (any, error) { return b.driver.ListModelCopyJobs(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ModelCopyJob), nil
}

// --- Evaluation jobs ---

// CreateEvaluationJob starts a model-evaluation job.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreateEvaluationJob(ctx context.Context, cfg driver.EvaluationJobConfig) (*driver.EvaluationJob, error) {
	out, err := b.do(ctx, "CreateEvaluationJob", cfg.JobName, func() (any, error) {
		return b.driver.CreateEvaluationJob(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EvaluationJob), nil
}

// GetEvaluationJob retrieves an evaluation job by name or ARN.
func (b *Bedrock) GetEvaluationJob(ctx context.Context, jobIdentifier string) (*driver.EvaluationJob, error) {
	out, err := b.do(ctx, "GetEvaluationJob", jobIdentifier, func() (any, error) {
		return b.driver.GetEvaluationJob(ctx, jobIdentifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EvaluationJob), nil
}

// ListEvaluationJobs lists all evaluation jobs.
func (b *Bedrock) ListEvaluationJobs(ctx context.Context) ([]driver.EvaluationJob, error) {
	out, err := b.do(ctx, "ListEvaluationJobs", nil, func() (any, error) { return b.driver.ListEvaluationJobs(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.EvaluationJob), nil
}

// StopEvaluationJob transitions an evaluation job to the Stopped state.
func (b *Bedrock) StopEvaluationJob(ctx context.Context, jobIdentifier string) error {
	_, err := b.do(ctx, "StopEvaluationJob", jobIdentifier, func() (any, error) {
		return nil, b.driver.StopEvaluationJob(ctx, jobIdentifier)
	})

	return err
}
