package driver

import "context"

// ChatMessage is one turn in a chat-completions conversation.
type ChatMessage struct {
	Role    string // system | user | assistant | tool
	Content string
}

// ChatCompletionRequest is an Azure OpenAI chat-completions request.
type ChatCompletionRequest struct {
	Messages    []ChatMessage
	Temperature *float64
	MaxTokens   *int
}

// ChatChoice is one completion choice.
type ChatChoice struct {
	Index        int
	Message      ChatMessage
	FinishReason string
}

// TokenUsage is the prompt/completion/total token accounting.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ChatCompletionResponse is an Azure OpenAI chat-completions response.
type ChatCompletionResponse struct {
	ID      string
	Model   string
	Created int64
	Choices []ChatChoice
	Usage   TokenUsage
}

// EmbeddingsRequest is an Azure OpenAI embeddings request.
type EmbeddingsRequest struct {
	Input []string
}

// EmbeddingData is one input's embedding vector.
type EmbeddingData struct {
	Index     int
	Embedding []float64
}

// EmbeddingsResponse is an Azure OpenAI embeddings response.
type EmbeddingsResponse struct {
	Model string
	Data  []EmbeddingData
	Usage TokenUsage
}

// CompletionsRequest is a (legacy) Azure OpenAI completions request.
type CompletionsRequest struct {
	Prompt    string
	MaxTokens *int
}

// CompletionChoice is one legacy-completion choice.
type CompletionChoice struct {
	Text         string
	Index        int
	FinishReason string
}

// CompletionsResponse is a legacy Azure OpenAI completions response.
type CompletionsResponse struct {
	ID      string
	Model   string
	Created int64
	Choices []CompletionChoice
	Usage   TokenUsage
}

// AssistantConfig describes an AI Foundry / Azure OpenAI assistant to create.
type AssistantConfig struct {
	Account      string
	Model        string
	Name         string
	Instructions string
}

// Assistant is an AI Foundry / Azure OpenAI assistant.
type Assistant struct {
	ID           string
	Model        string
	Name         string
	Instructions string
	CreatedAt    int64
}

// Thread is an assistants-API conversation thread.
type Thread struct {
	ID        string
	CreatedAt int64
}

// ThreadMessage is a message within a thread.
type ThreadMessage struct {
	ID        string
	ThreadID  string
	Role      string
	Content   string
	CreatedAt int64
}

// Run is an assistant run over a thread.
type Run struct {
	ID          string
	ThreadID    string
	AssistantID string
	Status      string // queued | in_progress | completed | failed
	CreatedAt   int64
}

// DataPlane is the Azure AI data-plane surface: Azure OpenAI inference, the AI
// Foundry Agents/Assistants API, and AML online-endpoint scoring. The account
// scopes inference and assistant state; deployment names the model deployment.
type DataPlane interface {
	// Azure OpenAI inference.
	ChatCompletions(ctx context.Context, account, deployment string, req ChatCompletionRequest) (*ChatCompletionResponse, error)
	Embeddings(ctx context.Context, account, deployment string, req EmbeddingsRequest) (*EmbeddingsResponse, error)
	Completions(ctx context.Context, account, deployment string, req CompletionsRequest) (*CompletionsResponse, error)

	// Agents / Assistants.
	CreateAssistant(ctx context.Context, cfg AssistantConfig) (*Assistant, error)
	GetAssistant(ctx context.Context, account, id string) (*Assistant, error)
	ListAssistants(ctx context.Context, account string) ([]Assistant, error)
	DeleteAssistant(ctx context.Context, account, id string) error
	CreateThread(ctx context.Context, account string) (*Thread, error)
	GetThread(ctx context.Context, account, id string) (*Thread, error)
	DeleteThread(ctx context.Context, account, id string) error
	CreateMessage(ctx context.Context, account, thread, role, content string) (*ThreadMessage, error)
	ListMessages(ctx context.Context, account, thread string) ([]ThreadMessage, error)
	CreateRun(ctx context.Context, account, thread, assistant string) (*Run, error)
	GetRun(ctx context.Context, account, thread, id string) (*Run, error)

	// AML online-endpoint scoring.
	ScoreOnlineEndpoint(ctx context.Context, endpoint string, body []byte) ([]byte, error)
}
