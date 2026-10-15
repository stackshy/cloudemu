package driver

import "context"

// OptimizedPrompt is the analysis of a prompt and its rewrite for a model.
type OptimizedPrompt struct {
	Analysis string
	Text     string
}

// PromptOptimizer rewrites a prompt for a target model.
type PromptOptimizer interface {
	OptimizePrompt(ctx context.Context, text, targetModelID string) (*OptimizedPrompt, error)
}
