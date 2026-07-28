package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreatePrompt creates a prompt at the DRAFT version.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreatePrompt(_ context.Context, cfg driver.PromptConfig) (*driver.Prompt, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "name is required")
	}

	id := idgen.GenerateID("PROMPT")
	now := m.now()
	prompt := &driver.Prompt{
		ID:                       id,
		ARN:                      idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "prompt/"+id),
		Name:                     cfg.Name,
		Description:              cfg.Description,
		Version:                  driver.DraftVersion,
		DefaultVariant:           cfg.DefaultVariant,
		CustomerEncryptionKeyArn: cfg.CustomerEncryptionKeyArn,
		Variants:                 copyRaw(cfg.Variants),
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	m.prompts.Set(id, prompt)

	result := *prompt

	return &result, nil
}

// GetPrompt returns a prompt by identifier.
func (m *Mock) GetPrompt(_ context.Context, id string) (*driver.Prompt, error) {
	prompt, ok := m.prompts.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	result := *prompt

	return &result, nil
}

// ListPrompts lists all prompts.
func (m *Mock) ListPrompts(_ context.Context) ([]driver.Prompt, error) {
	all := m.prompts.SortedValues()
	out := make([]driver.Prompt, 0, len(all))

	for _, p := range all {
		out = append(out, *p)
	}

	return out, nil
}

// UpdatePrompt updates a prompt's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdatePrompt(_ context.Context, id string, cfg driver.PromptConfig) (*driver.Prompt, error) {
	prompt, ok := m.prompts.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	updated := *prompt
	updated.Name = orDefault(cfg.Name, prompt.Name)
	updated.Description = cfg.Description
	updated.DefaultVariant = orDefault(cfg.DefaultVariant, prompt.DefaultVariant)
	updated.UpdatedAt = m.now()

	if len(cfg.Variants) != 0 {
		updated.Variants = copyRaw(cfg.Variants)
	}

	m.prompts.Set(id, &updated)

	result := updated

	return &result, nil
}

// DeletePrompt deletes a prompt and returns its identifier.
func (m *Mock) DeletePrompt(_ context.Context, id string) (string, error) {
	if !m.prompts.Has(id) {
		return "", errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	m.prompts.Delete(id)

	return id, nil
}
