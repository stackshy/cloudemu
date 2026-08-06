package ecr

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// SetRepositoryPolicy stores a repository's resource (permissions) policy.
func (m *Mock) SetRepositoryPolicy(_ context.Context, repository, policyText string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	rd.repoPolicy = policyText

	return rd.repoPolicy, nil
}

// GetRepositoryPolicy returns a repository's resource policy, or NotFound if
// none is set.
func (m *Mock) GetRepositoryPolicy(_ context.Context, repository string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	if rd.repoPolicy == "" {
		return "", errors.Newf(errors.NotFound, "no repository policy for %q", repository)
	}

	return rd.repoPolicy, nil
}

// DeleteRepositoryPolicy removes a repository's resource policy.
func (m *Mock) DeleteRepositoryPolicy(_ context.Context, repository string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", errors.Newf(errors.NotFound, "repository %q not found", repository)
	}

	if rd.repoPolicy == "" {
		return "", errors.Newf(errors.NotFound, "no repository policy for %q", repository)
	}

	policy := rd.repoPolicy
	rd.repoPolicy = ""

	return policy, nil
}
