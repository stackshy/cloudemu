package driver

import "context"

// Part is a single content part (text only is modeled).
type Part struct {
	Text string
}

// Content is one turn in a generateContent exchange.
type Content struct {
	Role  string // "user" | "model"
	Parts []Part
}

// GenerationConfig holds optional sampling controls.
type GenerationConfig struct {
	Temperature     *float64
	TopP            *float64
	TopK            *int
	MaxOutputTokens *int
}

// GenerateContentRequest is the Gemini generateContent request.
type GenerateContentRequest struct {
	Model             string
	Contents          []Content
	SystemInstruction *Content
	GenerationConfig  *GenerationConfig
}

// Candidate is one generated response candidate.
type Candidate struct {
	Content      Content
	FinishReason string
}

// UsageMetadata reports token accounting for a generateContent call.
type UsageMetadata struct {
	PromptTokenCount     int
	CandidatesTokenCount int
	TotalTokenCount      int
}

// GenerateContentResponse is the Gemini generateContent response.
type GenerateContentResponse struct {
	Candidates    []Candidate
	UsageMetadata UsageMetadata
}

// CountTokensResponse reports the token count for an input.
type CountTokensResponse struct {
	TotalTokens int
}

// TuningJobConfig describes a Gemini supervised-tuning job.
type TuningJobConfig struct {
	Location        string
	BaseModel       string
	TunedModelName  string
	TrainingDataURI string
}

// TuningJob describes a tuning job (create is synchronous; poll State).
type TuningJob struct {
	Name           string // projects/{p}/locations/{l}/tuningJobs/{id}
	BaseModel      string
	State          string
	TunedModelName string
	Endpoint       string
	CreateTime     string
	EndTime        string
}

// CachedContentConfig describes a context-cache entry to create.
type CachedContentConfig struct {
	Location          string
	Model             string
	DisplayName       string
	Contents          []Content
	SystemInstruction *Content
	TTLSeconds        int
}

// CachedContent is a context-cache entry (synchronous CRUD).
type CachedContent struct {
	Name        string // projects/{p}/locations/{l}/cachedContents/{id}
	Model       string
	DisplayName string
	CreateTime  string
	ExpireTime  string
}

// genAIAPI covers the Gemini runtime plus tuning jobs and context caching.
type genAIAPI interface {
	GenerateContent(ctx context.Context, model string, req GenerateContentRequest) (*GenerateContentResponse, error)
	CountTokens(ctx context.Context, model string, req GenerateContentRequest) (*CountTokensResponse, error)

	CreateTuningJob(ctx context.Context, cfg TuningJobConfig) (*TuningJob, error)
	GetTuningJob(ctx context.Context, name string) (*TuningJob, error)
	ListTuningJobs(ctx context.Context, location string) ([]TuningJob, error)
	CancelTuningJob(ctx context.Context, name string) error

	CreateCachedContent(ctx context.Context, cfg CachedContentConfig) (*CachedContent, error)
	GetCachedContent(ctx context.Context, name string) (*CachedContent, error)
	ListCachedContents(ctx context.Context, location string) ([]CachedContent, error)
	DeleteCachedContent(ctx context.Context, name string) error
}
