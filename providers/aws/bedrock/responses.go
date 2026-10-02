package bedrock

// Model-native InvokeModel response envelopes. Each family ships the JSON
// shape its real model returns, so SDK callers can deserialize the emulated
// inference output exactly as they would against AWS.

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Model      string             `json:"model"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type titanResult struct {
	TokenCount       int    `json:"tokenCount"`
	OutputText       string `json:"outputText"`
	CompletionReason string `json:"completionReason"`
}

type titanResponse struct {
	InputTextTokenCount int           `json:"inputTextTokenCount"`
	Results             []titanResult `json:"results"`
}

type llamaResponse struct {
	Generation           string `json:"generation"`
	PromptTokenCount     int    `json:"prompt_token_count"`
	GenerationTokenCount int    `json:"generation_token_count"`
	StopReason           string `json:"stop_reason"`
}

type cohereGeneration struct {
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
}

type cohereResponse struct {
	Generations []cohereGeneration `json:"generations"`
}

type novaContent struct {
	Text string `json:"text"`
}

type novaMessage struct {
	Role    string        `json:"role"`
	Content []novaContent `json:"content"`
}

type novaOutput struct {
	Message novaMessage `json:"message"`
}

type novaUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// novaResponse is the Amazon Nova InvokeModel envelope.
type novaResponse struct {
	Output     novaOutput `json:"output"`
	StopReason string     `json:"stopReason"`
	Usage      novaUsage  `json:"usage"`
}

type mistralOutput struct {
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
}

// mistralResponse is the Mistral text-completion envelope.
type mistralResponse struct {
	Outputs []mistralOutput `json:"outputs"`
}

// cohereRResponse is the Cohere Command R and R+ chat envelope.
type cohereRResponse struct {
	Text         string `json:"text"`
	GenerationID string `json:"generation_id"`
	FinishReason string `json:"finish_reason"`
}

type deepSeekChoice struct {
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
}

// deepSeekResponse is the DeepSeek-R1 InvokeModel envelope.
type deepSeekResponse struct {
	Choices []deepSeekChoice `json:"choices"`
}

// cohereEmbedResponse is the Cohere Embed v3 envelope.
type cohereEmbedResponse struct {
	ID           string      `json:"id"`
	ResponseType string      `json:"response_type"`
	Embeddings   [][]float64 `json:"embeddings"`
}

// imageResponse is the Titan Image Generator envelope: base64 images.
type imageResponse struct {
	Images []string `json:"images"`
}

type genericResponse struct {
	Completion string `json:"completion"`
	StopReason string `json:"stop_reason"`
}

// embeddingResponse is the envelope returned by embedding models (e.g. Titan
// Embeddings): a vector plus the input token count.
type embeddingResponse struct {
	Embedding           []float64 `json:"embedding"`
	InputTextTokenCount int       `json:"inputTextTokenCount"`
}
