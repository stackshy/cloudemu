package chaos

import (
	"context"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// chaosVertexAI wraps a Vertex AI service. It consults the engine on the calls
// most worth failing in tests — job and pipeline submission, model deployment,
// the online prediction and generateContent runtimes, and Feature Store online
// serving — and delegates every other operation through the embedded
// driver.VertexAI unchanged.
type chaosVertexAI struct {
	driver.VertexAI
	engine *Engine
}

// WrapVertexAI returns a Vertex AI service that injects chaos on submission and
// runtime calls. service+operation pairs use the "vertexai" service name.
func WrapVertexAI(inner driver.VertexAI, engine *Engine) driver.VertexAI {
	return &chaosVertexAI{VertexAI: inner, engine: engine}
}

func (c *chaosVertexAI) CreateCustomJob(ctx context.Context, cfg driver.CustomJobConfig) (*driver.CustomJob, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreateCustomJob"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreateCustomJob(ctx, cfg)
}

func (c *chaosVertexAI) CreateHyperparameterTuningJob(
	ctx context.Context, cfg driver.HyperparameterTuningJobConfig,
) (*driver.HyperparameterTuningJob, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreateHyperparameterTuningJob"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreateHyperparameterTuningJob(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosVertexAI) CreateBatchPredictionJob(
	ctx context.Context, cfg driver.BatchPredictionJobConfig,
) (*driver.BatchPredictionJob, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreateBatchPredictionJob"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreateBatchPredictionJob(ctx, cfg)
}

func (c *chaosVertexAI) CreateTrainingPipeline(
	ctx context.Context, cfg driver.TrainingPipelineConfig,
) (*driver.TrainingPipeline, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreateTrainingPipeline"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreateTrainingPipeline(ctx, cfg)
}

func (c *chaosVertexAI) CreatePipelineJob(ctx context.Context, cfg driver.PipelineJobConfig) (*driver.PipelineJob, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreatePipelineJob"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreatePipelineJob(ctx, cfg)
}

func (c *chaosVertexAI) CreateTuningJob(ctx context.Context, cfg driver.TuningJobConfig) (*driver.TuningJob, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "CreateTuningJob"); err != nil {
		return nil, err
	}

	return c.VertexAI.CreateTuningJob(ctx, cfg)
}

//nolint:gocritic // dm matches the driver signature; delegated unchanged on success.
func (c *chaosVertexAI) DeployModel(
	ctx context.Context, endpoint string, dm driver.DeployedModel,
) (*driver.Operation, *driver.Endpoint, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "DeployModel"); err != nil {
		return nil, nil, err
	}

	return c.VertexAI.DeployModel(ctx, endpoint, dm)
}

func (c *chaosVertexAI) Predict(ctx context.Context, req driver.PredictRequest) (*driver.PredictResponse, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "Predict"); err != nil {
		return nil, err
	}

	return c.VertexAI.Predict(ctx, req)
}

func (c *chaosVertexAI) GenerateContent(
	ctx context.Context, model string, req driver.GenerateContentRequest,
) (*driver.GenerateContentResponse, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "GenerateContent"); err != nil {
		return nil, err
	}

	return c.VertexAI.GenerateContent(ctx, model, req)
}

func (c *chaosVertexAI) WriteFeatureValues(
	ctx context.Context, entityType, entityID string, values []driver.FeatureNameValue,
) error {
	if err := applyChaos(ctx, c.engine, "vertexai", "WriteFeatureValues"); err != nil {
		return err
	}

	return c.VertexAI.WriteFeatureValues(ctx, entityType, entityID, values)
}

func (c *chaosVertexAI) FetchFeatureValues(
	ctx context.Context, featureView, entityID string,
) ([]driver.FeatureNameValue, error) {
	if err := applyChaos(ctx, c.engine, "vertexai", "FetchFeatureValues"); err != nil {
		return nil, err
	}

	return c.VertexAI.FetchFeatureValues(ctx, featureView, entityID)
}
