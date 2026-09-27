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
	if err := validatePrompt(cfg); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	id := newID(m.prompts)
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
	m.putTags(prompt.ARN, cfg.Tags)

	result := clonePrompt(prompt)

	return &result, nil
}

// GetPrompt returns a prompt by identifier.
func (m *Mock) GetPrompt(_ context.Context, id string) (*driver.Prompt, error) {
	prompt, ok := m.prompts.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	result := clonePrompt(prompt)

	return &result, nil
}

// ListPrompts lists one page of prompts.
func (m *Mock) ListPrompts(_ context.Context, page driver.Page) ([]driver.Prompt, string, error) {
	all := m.prompts.SortedValues()
	out := make([]driver.Prompt, 0, len(all))

	for _, p := range all {
		out = append(out, clonePrompt(p))
	}

	return paginate(out, page)
}

// UpdatePrompt updates a prompt's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdatePrompt(_ context.Context, id string, cfg driver.PromptConfig) (*driver.Prompt, error) {
	if err := validatePrompt(cfg); err != nil {
		return nil, err
	}

	prompt, ok := m.prompts.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	updated := *prompt
	updated.Name = cfg.Name
	updated.Description = cfg.Description
	updated.DefaultVariant = orDefault(cfg.DefaultVariant, prompt.DefaultVariant)
	updated.UpdatedAt = m.now()

	if len(cfg.Variants) != 0 {
		updated.Variants = copyRaw(cfg.Variants)
	}

	m.prompts.Set(id, &updated)

	result := clonePrompt(&updated)

	return &result, nil
}

// DeletePrompt deletes a prompt and its tags and returns its identifier.
func (m *Mock) DeletePrompt(_ context.Context, id string) (string, error) {
	prompt, ok := m.prompts.Get(id)
	if !ok {
		return "", errors.Newf(errors.NotFound, "prompt %q not found", id)
	}

	m.prompts.Delete(id)
	m.dropTags(prompt.ARN)

	return id, nil
}

// validatePrompt checks the member CreatePrompt and UpdatePrompt require.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validatePrompt(cfg driver.PromptConfig) error {
	var v violations

	v.required("name", cfg.Name == "")

	return v.err()
}

// clonePrompt returns a value copy whose Variants do not alias the stored
// prompt, so callers can't mutate internal state via the result.
func clonePrompt(p *driver.Prompt) driver.Prompt {
	out := *p
	out.Variants = copyRaw(p.Variants)

	return out
}
