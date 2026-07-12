package chaos

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// chaosSageMaker wraps a SageMaker service. It consults the engine on the
// calls most worth failing in tests — job submission, endpoint creation, the
// inference runtime, and the Feature Store online store — and delegates every
// other operation through the embedded driver.Service unchanged.
type chaosSageMaker struct {
	driver.Service
	engine *Engine
}

// WrapSageMaker returns a SageMaker service that injects chaos on submission
// and runtime calls. service+operation pairs use the "sagemaker" service name.
func WrapSageMaker(inner driver.Service, engine *Engine) driver.Service {
	return &chaosSageMaker{Service: inner, engine: engine}
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosSageMaker) CreateTrainingJob(ctx context.Context, cfg driver.TrainingJobConfig) (*driver.TrainingJob, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "CreateTrainingJob"); err != nil {
		return nil, err
	}

	return c.Service.CreateTrainingJob(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosSageMaker) CreateProcessingJob(ctx context.Context, cfg driver.ProcessingJobConfig) (*driver.ProcessingJob, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "CreateProcessingJob"); err != nil {
		return nil, err
	}

	return c.Service.CreateProcessingJob(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosSageMaker) CreateTransformJob(ctx context.Context, cfg driver.TransformJobConfig) (*driver.TransformJob, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "CreateTransformJob"); err != nil {
		return nil, err
	}

	return c.Service.CreateTransformJob(ctx, cfg)
}

func (c *chaosSageMaker) CreateEndpoint(ctx context.Context, cfg driver.EndpointSpec) (*driver.Endpoint, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "CreateEndpoint"); err != nil {
		return nil, err
	}

	return c.Service.CreateEndpoint(ctx, cfg)
}

//nolint:gocritic // in matches the driver signature; delegated unchanged on success.
func (c *chaosSageMaker) InvokeEndpoint(ctx context.Context, in driver.InvokeEndpointInput) (*driver.InvokeEndpointOutput, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "InvokeEndpoint"); err != nil {
		return nil, err
	}

	return c.Service.InvokeEndpoint(ctx, in)
}

func (c *chaosSageMaker) InvokeEndpointAsync(
	ctx context.Context, in driver.InvokeEndpointAsyncInput,
) (*driver.InvokeEndpointAsyncOutput, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "InvokeEndpointAsync"); err != nil {
		return nil, err
	}

	return c.Service.InvokeEndpointAsync(ctx, in)
}

func (c *chaosSageMaker) PutRecord(ctx context.Context, groupName string, record []driver.FeatureValue) error {
	if err := applyChaos(ctx, c.engine, "sagemaker", "PutRecord"); err != nil {
		return err
	}

	return c.Service.PutRecord(ctx, groupName, record)
}

func (c *chaosSageMaker) GetRecord(ctx context.Context, groupName, recordID string) ([]driver.FeatureValue, error) {
	if err := applyChaos(ctx, c.engine, "sagemaker", "GetRecord"); err != nil {
		return nil, err
	}

	return c.Service.GetRecord(ctx, groupName, recordID)
}
