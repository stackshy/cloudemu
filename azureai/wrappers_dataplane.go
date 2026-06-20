package azureai

import (
	"context"

	"github.com/stackshy/cloudemu/azureai/driver"
)

func (a *AzureAI) ChatCompletions(
	ctx context.Context, account, deployment string, req driver.ChatCompletionRequest,
) (*driver.ChatCompletionResponse, error) {
	return cast[*driver.ChatCompletionResponse](a.do(ctx, "ChatCompletions", deployment, func() (any, error) {
		return a.drv.ChatCompletions(ctx, account, deployment, req)
	}))
}

func (a *AzureAI) Embeddings(
	ctx context.Context, account, deployment string, req driver.EmbeddingsRequest,
) (*driver.EmbeddingsResponse, error) {
	return cast[*driver.EmbeddingsResponse](a.do(ctx, "Embeddings", deployment, func() (any, error) {
		return a.drv.Embeddings(ctx, account, deployment, req)
	}))
}

func (a *AzureAI) Completions(
	ctx context.Context, account, deployment string, req driver.CompletionsRequest,
) (*driver.CompletionsResponse, error) {
	return cast[*driver.CompletionsResponse](a.do(ctx, "Completions", deployment, func() (any, error) {
		return a.drv.Completions(ctx, account, deployment, req)
	}))
}

func (a *AzureAI) CreateAssistant(ctx context.Context, cfg driver.AssistantConfig) (*driver.Assistant, error) {
	return cast[*driver.Assistant](a.do(ctx, "CreateAssistant", cfg, func() (any, error) { return a.drv.CreateAssistant(ctx, cfg) }))
}

func (a *AzureAI) GetAssistant(ctx context.Context, account, id string) (*driver.Assistant, error) {
	return cast[*driver.Assistant](a.do(ctx, "GetAssistant", id, func() (any, error) { return a.drv.GetAssistant(ctx, account, id) }))
}

func (a *AzureAI) ListAssistants(ctx context.Context, account string) ([]driver.Assistant, error) {
	return cast[[]driver.Assistant](a.do(ctx, "ListAssistants", account, func() (any, error) {
		return a.drv.ListAssistants(ctx, account)
	}))
}

func (a *AzureAI) DeleteAssistant(ctx context.Context, account, id string) error {
	return a.act(ctx, "DeleteAssistant", id, func() error { return a.drv.DeleteAssistant(ctx, account, id) })
}

func (a *AzureAI) CreateThread(ctx context.Context, account string) (*driver.Thread, error) {
	return cast[*driver.Thread](a.do(ctx, "CreateThread", account, func() (any, error) { return a.drv.CreateThread(ctx, account) }))
}

func (a *AzureAI) GetThread(ctx context.Context, account, id string) (*driver.Thread, error) {
	return cast[*driver.Thread](a.do(ctx, "GetThread", id, func() (any, error) { return a.drv.GetThread(ctx, account, id) }))
}

func (a *AzureAI) DeleteThread(ctx context.Context, account, id string) error {
	return a.act(ctx, "DeleteThread", id, func() error { return a.drv.DeleteThread(ctx, account, id) })
}

func (a *AzureAI) CreateMessage(ctx context.Context, account, thread, role, content string) (*driver.ThreadMessage, error) {
	return cast[*driver.ThreadMessage](a.do(ctx, "CreateMessage", thread, func() (any, error) {
		return a.drv.CreateMessage(ctx, account, thread, role, content)
	}))
}

func (a *AzureAI) ListMessages(ctx context.Context, account, thread string) ([]driver.ThreadMessage, error) {
	return cast[[]driver.ThreadMessage](a.do(ctx, "ListMessages", thread, func() (any, error) {
		return a.drv.ListMessages(ctx, account, thread)
	}))
}

func (a *AzureAI) CreateRun(ctx context.Context, account, thread, assistant string) (*driver.Run, error) {
	return cast[*driver.Run](a.do(ctx, "CreateRun", thread, func() (any, error) {
		return a.drv.CreateRun(ctx, account, thread, assistant)
	}))
}

func (a *AzureAI) GetRun(ctx context.Context, account, thread, id string) (*driver.Run, error) {
	return cast[*driver.Run](a.do(ctx, "GetRun", id, func() (any, error) { return a.drv.GetRun(ctx, account, thread, id) }))
}

func (a *AzureAI) ScoreOnlineEndpoint(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return cast[[]byte](a.do(ctx, "ScoreOnlineEndpoint", endpoint, func() (any, error) {
		return a.drv.ScoreOnlineEndpoint(ctx, endpoint, body)
	}))
}
