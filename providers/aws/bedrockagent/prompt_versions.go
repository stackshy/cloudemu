package bedrockagent

import (
	"context"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreatePromptVersion publishes the prompt's DRAFT as the next numbered
// version, whose ARN is the prompt ARN with a ":<version>" suffix.
func (m *Mock) CreatePromptVersion(_ context.Context, promptID, description string, tags map[string]string) (*driver.Prompt, error) {
	promptID, _ = splitPromptIdentifier(promptID)

	var v violations

	v.description(description)

	if err := v.err(); err != nil {
		return nil, err
	}

	if err := validateTags(tags); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	draft, ok := m.prompts.Get(promptID)
	if !ok {
		return nil, promptNotFound(promptID)
	}

	snap := clonePrompt(draft)
	snap.Version = m.nextVersion("prompt/" + promptID)
	snap.ARN = draft.ARN + ":" + snap.Version
	snap.Description = orDefault(description, draft.Description)
	snap.CreatedAt = m.now()
	snap.UpdatedAt = snap.CreatedAt
	m.promptVersions.Set(versionKey(promptID, snap.Version), &snap)
	m.putTags(snap.ARN, tags)

	result := clonePrompt(&snap)

	return &result, nil
}

// GetPromptVersion returns DRAFT or a numbered version of a prompt.
func (m *Mock) GetPromptVersion(_ context.Context, promptID, version string) (*driver.Prompt, error) {
	promptID, _ = splitPromptIdentifier(promptID)

	var v violations

	v.version("promptVersion", version)

	if err := v.err(); err != nil {
		return nil, err
	}

	draft, ok := m.prompts.Get(promptID)
	if !ok {
		return nil, promptNotFound(promptID)
	}

	if version == driver.DraftVersion {
		result := clonePrompt(draft)

		return &result, nil
	}

	stored, ok := m.promptVersions.Get(versionKey(promptID, version))
	if !ok {
		return nil, versionNotFound("prompt", promptID, version)
	}

	result := clonePrompt(stored)

	return &result, nil
}

// ListPromptVersions lists one page of a prompt's DRAFT and numbered versions.
func (m *Mock) ListPromptVersions(_ context.Context, promptID string, page driver.Page) ([]driver.Prompt, string, error) {
	promptID, _ = splitPromptIdentifier(promptID)

	draft, ok := m.prompts.Get(promptID)
	if !ok {
		return nil, "", promptNotFound(promptID)
	}

	out := append([]driver.Prompt{clonePrompt(draft)}, childrenOf(m.promptVersions, promptID, clonePrompt)...)
	sortByVersion(out, func(p driver.Prompt) string { return p.Version })

	return paginate(out, page)
}

// DeletePromptVersion deletes one numbered version of a prompt and its tags,
// leaving the prompt and its other versions.
func (m *Mock) DeletePromptVersion(_ context.Context, promptID, version string) error {
	promptID, suffix := splitPromptIdentifier(promptID)

	var v violations

	if suffix != "" && suffix != version {
		v.addf("promptIdentifier names version %s but promptVersion is %s", suffix, version)
	}

	v.numericVersion("promptVersion", version)

	if err := v.err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.prompts.Has(promptID) {
		return promptNotFound(promptID)
	}

	key := versionKey(promptID, version)

	stored, ok := m.promptVersions.Get(key)
	if !ok {
		return versionNotFound("prompt", promptID, version)
	}

	m.promptVersions.Delete(key)
	m.dropTags(stored.ARN)

	return nil
}

// deletePromptChildren removes a deleted prompt's versions and their tags.
// Callers hold m.mu.
func (m *Mock) deletePromptChildren(promptID string) {
	for key, p := range m.promptVersions.All() {
		if strings.HasPrefix(key, promptID+"/") {
			m.promptVersions.Delete(key)
			m.dropTags(p.ARN)
		}
	}

	m.versionSeq.Delete("prompt/" + promptID)
}

// splitPromptIdentifier turns a prompt id or ARN (optionally ending in
// ":<version>") into the prompt id and that version.
func splitPromptIdentifier(identifier string) (id, version string) {
	rest := resourceID(identifier, "prompt/")
	id, version, _ = strings.Cut(rest, ":")

	return id, version
}

// resourceID returns the id of an id-or-ARN identifier: whatever follows kind
// ("flow/", "prompt/") in an ARN, else the identifier itself.
func resourceID(identifier, kind string) string {
	if !strings.HasPrefix(identifier, "arn:") {
		return identifier
	}

	_, rest, ok := strings.Cut(identifier, ":"+kind)
	if !ok {
		return identifier
	}

	return rest
}

// sortByVersion orders DRAFT first, then numbered versions ascending.
func sortByVersion[T any](items []T, version func(T) string) {
	slices.SortStableFunc(items, func(a, b T) int { return compareVersions(version(a), version(b)) })
}

func promptNotFound(id string) error {
	return errors.Newf(errors.NotFound, "prompt %q not found", id)
}
