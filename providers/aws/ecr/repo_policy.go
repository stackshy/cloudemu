package ecr

import "context"

// SetRepositoryPolicy stores a repository's resource (permissions) policy. It
// returns the owning registryId alongside the policy text: real ECR echoes
// registryId on every repository-policy response.
func (m *Mock) SetRepositoryPolicy(_ context.Context, repository, policyText string) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", "", apiErrf(excRepositoryNotFound, "repository %q not found", repository)
	}

	rd.repoPolicy = policyText

	return rd.info.RegistryID, rd.repoPolicy, nil
}

// GetRepositoryPolicy returns a repository's resource policy and owning
// registryId, or NotFound if none is set.
func (m *Mock) GetRepositoryPolicy(_ context.Context, repository string) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", "", apiErrf(excRepositoryNotFound, "repository %q not found", repository)
	}

	if rd.repoPolicy == "" {
		return "", "", apiErrf(excRepositoryPolicyNotFound, "no repository policy for %q", repository)
	}

	return rd.info.RegistryID, rd.repoPolicy, nil
}

// DeleteRepositoryPolicy removes a repository's resource policy and returns
// the owning registryId alongside the policy that was deleted.
func (m *Mock) DeleteRepositoryPolicy(_ context.Context, repository string) (registryID, policy string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.repos.Get(repository)
	if !ok {
		return "", "", apiErrf(excRepositoryNotFound, "repository %q not found", repository)
	}

	if rd.repoPolicy == "" {
		return "", "", apiErrf(excRepositoryPolicyNotFound, "no repository policy for %q", repository)
	}

	policy = rd.repoPolicy
	rd.repoPolicy = ""

	return rd.info.RegistryID, policy, nil
}
