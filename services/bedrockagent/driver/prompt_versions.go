package driver

import "context"

// PromptVersions publishes immutable numbered versions of a prompt's DRAFT.
// Identifiers may be ids or ARNs. ListPromptVersions lists DRAFT and every
// version of one prompt.
type PromptVersions interface {
	CreatePromptVersion(ctx context.Context, promptID, description string, tags map[string]string) (*Prompt, error)
	GetPromptVersion(ctx context.Context, promptID, version string) (*Prompt, error)
	ListPromptVersions(ctx context.Context, promptID string, page Page) ([]Prompt, string, error)
	DeletePromptVersion(ctx context.Context, promptID, version string) error
}
