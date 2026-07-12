package chaos

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

// chaosAzureAI wraps an Azure AI service. It consults the engine on the calls
// most worth failing in tests — account/workspace and deployment/job creation,
// the inference and scoring runtimes — and delegates every other operation
// through the embedded driver.AzureAI unchanged.
type chaosAzureAI struct {
	driver.AzureAI
	engine *Engine
}

// WrapAzureAI returns an Azure AI service that injects chaos on creation and
// runtime calls. service+operation pairs use the "azureai" service name.
func WrapAzureAI(inner driver.AzureAI, engine *Engine) driver.AzureAI {
	return &chaosAzureAI{AzureAI: inner, engine: engine}
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureAI) CreateAccount(ctx context.Context, cfg driver.AccountConfig) (*driver.Account, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "CreateAccount"); err != nil {
		return nil, err
	}

	return c.AzureAI.CreateAccount(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureAI) CreateDeployment(ctx context.Context, cfg driver.DeploymentConfig) (*driver.Deployment, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "CreateDeployment"); err != nil {
		return nil, err
	}

	return c.AzureAI.CreateDeployment(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureAI) CreateMLWorkspace(ctx context.Context, cfg driver.MLWorkspaceConfig) (*driver.MLWorkspace, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "CreateMLWorkspace"); err != nil {
		return nil, err
	}

	return c.AzureAI.CreateMLWorkspace(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureAI) CreateJob(ctx context.Context, cfg driver.JobConfig) (*driver.Job, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "CreateJob"); err != nil {
		return nil, err
	}

	return c.AzureAI.CreateJob(ctx, cfg)
}

//nolint:gocritic // cfg matches the driver signature; delegated unchanged on success.
func (c *chaosAzureAI) CreateEndpoint(ctx context.Context, cfg driver.EndpointConfig) (*driver.Endpoint, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "CreateEndpoint"); err != nil {
		return nil, err
	}

	return c.AzureAI.CreateEndpoint(ctx, cfg)
}

func (c *chaosAzureAI) ChatCompletions(
	ctx context.Context, account, deployment string, req driver.ChatCompletionRequest,
) (*driver.ChatCompletionResponse, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "ChatCompletions"); err != nil {
		return nil, err
	}

	return c.AzureAI.ChatCompletions(ctx, account, deployment, req)
}

func (c *chaosAzureAI) Embeddings(
	ctx context.Context, account, deployment string, req driver.EmbeddingsRequest,
) (*driver.EmbeddingsResponse, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "Embeddings"); err != nil {
		return nil, err
	}

	return c.AzureAI.Embeddings(ctx, account, deployment, req)
}

func (c *chaosAzureAI) ScoreOnlineEndpoint(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	if err := applyChaos(ctx, c.engine, "azureai", "ScoreOnlineEndpoint"); err != nil {
		return nil, err
	}

	return c.AzureAI.ScoreOnlineEndpoint(ctx, endpoint, body)
}
