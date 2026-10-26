package codeartifact

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// externalConnectionFormat returns the package format a supported public
// repository serves. These are the ten connections the CodeArtifact user guide
// lists ("Supported external connection repositories").
func externalConnectionFormat(name string) (string, bool) {
	switch name {
	case "public:maven-clojars", "public:maven-commonsware", "public:maven-googleandroid",
		"public:maven-gradleplugins", "public:maven-central":
		return driver.FormatMaven, true
	case "public:npmjs":
		return driver.FormatNPM, true
	case "public:nuget-org":
		return driver.FormatNuGet, true
	case "public:pypi":
		return driver.FormatPyPI, true
	case "public:ruby-gems-org":
		return driver.FormatRuby, true
	case "public:crates-io":
		return driver.FormatCargo, true
	default:
		return "", false
	}
}

// CreateRepository provisions a new repository in an existing domain with stable
// computed fields (arn, administratorAccount, domainOwner and createdTime). The
// parent domain must exist under the given owner or a ResourceNotFoundException
// is returned; a repository name already used in the domain yields a
// ConflictException; a domain holds at most 1000 repositories and a repository
// at most 10 upstreams, each of which must exist.
func (m *Mock) CreateRepository(_ context.Context, in *driver.CreateRepositoryInput) (*driver.Repository, error) {
	if err := validateRepositoryRef(driver.RepositoryRef{Domain: in.Domain, Owner: in.DomainOwner, Repository: in.Repository}); err != nil {
		return nil, err
	}

	if err := validateDescription(in.Description); err != nil {
		return nil, err
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	if err := validateUpstreamShape(in.Repository, in.Upstreams); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, err := m.domainLocked(driver.DomainRef{Domain: in.Domain, Owner: in.DomainOwner})
	if err != nil {
		return nil, err
	}

	key := repoKey(in.Domain, in.Repository)
	if m.repos.Has(key) {
		return nil, conflict(driver.ResourceTypeRepository, in.Repository,
			"Repository %s already exists in domain %s", in.Repository, in.Domain)
	}

	if m.repositoryCountLocked(in.Domain) >= maxReposPerDomain {
		return nil, quotaExceeded(driver.ResourceTypeRepository, in.Repository,
			"A domain can have at most %d repositories", maxReposPerDomain)
	}

	if upErr := m.validateUpstreamsLocked(in.Domain, in.Repository, in.Upstreams); upErr != nil {
		return nil, upErr
	}

	r := driver.Repository{
		Name:                 in.Repository,
		AdministratorAccount: m.opts.AccountID,
		DomainName:           in.Domain,
		DomainOwner:          d.Owner,
		Arn:                  m.repositoryARN(d.Owner, in.Domain, in.Repository),
		Description:          in.Description,
		CreatedTime:          m.now(),
		Upstreams:            copyUpstreams(in.Upstreams),
		Tags:                 copyTags(in.Tags),
	}

	m.repos.Set(key, r)

	out := copyRepository(&r)

	return &out, nil
}

// validateUpstreamShape checks the parts of an upstream list that need no stored
// state: valid names, no duplicates and not the repository itself. It runs before
// anything is looked up, so a malformed request is a ValidationException even
// when a quota is also exhausted.
func validateUpstreamShape(repo string, ups []driver.UpstreamRef) error {
	seen := make(map[string]bool, len(ups))

	for _, u := range ups {
		if err := validateRepositoryName(u.RepositoryName); err != nil {
			return err
		}

		if u.RepositoryName == repo {
			return validation("repository %s cannot be its own upstream", repo)
		}

		if seen[u.RepositoryName] {
			return validation("upstream repository %s is listed more than once", u.RepositoryName)
		}

		seen[u.RepositoryName] = true
	}

	return nil
}

// validateUpstreamsLocked checks a repository's upstream list against stored
// state: at most 10, each existing in the domain, and no cycle through the
// repositories already stored. The caller holds mu and has run
// validateUpstreamShape.
func (m *Mock) validateUpstreamsLocked(domain, repo string, ups []driver.UpstreamRef) error {
	if len(ups) > maxUpstreams {
		return quotaExceeded(driver.ResourceTypeRepository, repo, "A repository can have at most %d direct upstream repositories", maxUpstreams)
	}

	for _, u := range ups {
		if !m.repos.Has(repoKey(domain, u.RepositoryName)) {
			return notFound(driver.ResourceTypeRepository, u.RepositoryName,
				"Upstream repository %s not found in domain %s", u.RepositoryName, domain)
		}

		if m.reachesLocked(domain, u.RepositoryName, repo) {
			return validation("adding upstream %s to %s would create a cycle", u.RepositoryName, repo)
		}
	}

	return nil
}

// reachesLocked reports whether target is reachable from start through upstream
// links. The caller holds mu.
func (m *Mock) reachesLocked(domain, start, target string) bool {
	visited := map[string]bool{}
	queue := []string{start}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur == target {
			return true
		}

		if visited[cur] {
			continue
		}

		visited[cur] = true

		r, ok := m.repos.Get(repoKey(domain, cur))
		if !ok {
			continue
		}

		for _, u := range r.Upstreams {
			queue = append(queue, u.RepositoryName)
		}
	}

	return false
}

// DescribeRepository returns a copy of the repository. Stored computed fields are
// returned unchanged so repeated reads never drift.
func (m *Mock) DescribeRepository(_ context.Context, domain, domainOwner, repository string) (*driver.Repository, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, r, err := m.repositoryLocked(driver.RepositoryRef{Domain: domain, Owner: domainOwner, Repository: repository})
	if err != nil {
		return nil, err
	}

	out := copyRepository(&r)

	return &out, nil
}

// applyUpstreamsLocked replaces r's upstream list after checking it against stored
// state and the repository's external connection. The caller holds mu.
func (m *Mock) applyUpstreamsLocked(r *driver.Repository, ups []driver.UpstreamRef) error {
	if err := m.validateUpstreamsLocked(r.DomainName, r.Name, ups); err != nil {
		return err
	}

	if len(ups) > 0 && len(r.ExternalConnections) > 0 {
		return conflict(driver.ResourceTypeRepository, r.Name,
			"Repository %s has an external connection, so it cannot have upstream repositories", r.Name)
	}

	r.Upstreams = copyUpstreams(ups)

	return nil
}

// UpdateRepository replaces the description and/or upstreams the request
// supplied, leaving unmentioned fields untouched. The computed identity fields
// are preserved.
func (m *Mock) UpdateRepository(_ context.Context, in *driver.UpdateRepositoryInput) (*driver.Repository, error) {
	ref := driver.RepositoryRef{Domain: in.Domain, Owner: in.DomainOwner, Repository: in.Repository}

	if err := validateRepositoryRef(ref); err != nil {
		return nil, err
	}

	if in.Description != nil {
		if err := validateDescription(*in.Description); err != nil {
			return nil, err
		}
	}

	if in.Upstreams != nil {
		if err := validateUpstreamShape(in.Repository, *in.Upstreams); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	if in.Upstreams != nil {
		if upErr := m.applyUpstreamsLocked(&r, *in.Upstreams); upErr != nil {
			return nil, upErr
		}
	}

	if in.Description != nil {
		r.Description = *in.Description
	}

	m.repos.Set(repoKey(in.Domain, in.Repository), r)

	out := copyRepository(&r)

	return &out, nil
}

// DeleteRepository removes a repository with its packages, versions, assets and
// policy, and returns its description. A repository another repository lists as
// an upstream cannot be deleted (ConflictException). It is also dropped from
// the allowed-repository lists of the domain's package groups.
func (m *Mock) DeleteRepository(_ context.Context, domain, domainOwner, repository string) (*driver.Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(driver.RepositoryRef{Domain: domain, Owner: domainOwner, Repository: repository})
	if err != nil {
		return nil, err
	}

	others := m.repos.SortedValues()
	for i := range others {
		if others[i].DomainName != domain || others[i].Name == repository {
			continue
		}

		for _, u := range others[i].Upstreams {
			if u.RepositoryName == repository {
				return nil, conflict(driver.ResourceTypeRepository, repository,
					"Repository %s is an upstream of repository %s", repository, others[i].Name)
			}
		}
	}

	out := copyRepository(&r)
	key := repoKey(domain, repository)

	m.repos.Delete(key)
	m.repoPolicies.Delete(key)

	for pkgKey := range m.packages.Filter(func(k string, _ *packageRecord) bool { return strings.HasPrefix(k, key+"/") }) {
		m.packages.Delete(pkgKey)
	}

	m.dropAllowedRepositoryLocked(domain, repository)

	return &out, nil
}

// ListRepositories returns a deterministic page of all repositories ordered by
// their store key, optionally filtered by a name prefix.
func (m *Mock) ListRepositories(_ context.Context, prefix string,
	page driver.Page) ([]*driver.Repository, string, error) {
	if prefix != "" {
		if err := validateRepositoryName(prefix); err != nil {
			return nil, "", err
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.listReposLocked(prefix, page, func(driver.Repository) bool { return true })
}

// ListRepositoriesInDomain returns a deterministic page of the repositories in a
// single domain, optionally filtered by a name prefix.
func (m *Mock) ListRepositoriesInDomain(ctx context.Context, domain, domainOwner, prefix string,
	page driver.Page) ([]*driver.Repository, string, error) {
	return m.ListRepositoriesInDomainByAdministrator(ctx, driver.DomainRef{Domain: domain, Owner: domainOwner}, "", prefix, page)
}

// ListRepositoriesInDomainByAdministrator is ListRepositoriesInDomain narrowed to
// the repositories a given account administers ("" keeps all of them).
func (m *Mock) ListRepositoriesInDomainByAdministrator(_ context.Context, ref driver.DomainRef, administrator, prefix string,
	page driver.Page) ([]*driver.Repository, string, error) {
	if prefix != "" {
		if err := validateRepositoryName(prefix); err != nil {
			return nil, "", err
		}
	}

	if administrator != "" && !validOwner(administrator) {
		return nil, "", validation("administrator account %q must be a 12-digit account id", administrator)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.domainLocked(ref); err != nil {
		return nil, "", err
	}

	return m.listReposLocked(prefix, page, func(r driver.Repository) bool {
		return r.DomainName == ref.Domain && (administrator == "" || r.AdministratorAccount == administrator)
	})
}

// listReposLocked is the shared paginated repository lister. The caller holds mu.
func (m *Mock) listReposLocked(prefix string, page driver.Page,
	keep func(driver.Repository) bool) ([]*driver.Repository, string, error) {
	all := m.repos.SortedValues()
	filtered := make([]driver.Repository, 0, len(all))

	for i := range all {
		if keep(all[i]) && strings.HasPrefix(all[i].Name, prefix) {
			filtered = append(filtered, all[i])
		}
	}

	start, end, next, err := paginate(len(filtered), page)
	if err != nil {
		return nil, "", err
	}

	out := make([]*driver.Repository, 0, end-start)

	for i := start; i < end; i++ {
		r := copyRepository(&filtered[i])
		out = append(out, &r)
	}

	return out, next, nil
}

// AssociateExternalConnection adds an external connection to a repository. The
// connection must be one of the supported public repositories, a repository
// holds at most one, and a repository with upstream repositories cannot have one.
func (m *Mock) AssociateExternalConnection(_ context.Context, domain, domainOwner, repository,
	externalConnection string) (*driver.Repository, error) {
	ref := driver.RepositoryRef{Domain: domain, Owner: domainOwner, Repository: repository}

	if err := validateRepositoryRef(ref); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	format, ok := externalConnectionFormat(externalConnection)
	if !ok {
		return nil, notFound(driver.ResourceTypeRepository, externalConnection,
			"External connection %s is not a supported public repository", externalConnection)
	}

	if len(r.ExternalConnections) > 0 {
		return nil, conflict(driver.ResourceTypeRepository, repository,
			"Repository %s already has an external connection; only one is allowed", repository)
	}

	if len(r.Upstreams) > 0 {
		return nil, conflict(driver.ResourceTypeRepository, repository,
			"Repository %s has upstream repositories, so it cannot have an external connection", repository)
	}

	r.ExternalConnections = []driver.ExternalConnection{{
		ExternalConnectionName: externalConnection,
		PackageFormat:          format,
		Status:                 driver.ExternalConnStatusAvailable,
	}}

	m.repos.Set(repoKey(domain, repository), r)

	out := copyRepository(&r)

	return &out, nil
}

// DisassociateExternalConnection removes the named external connection from a
// repository. A connection that is not attached is a ResourceNotFoundException.
func (m *Mock) DisassociateExternalConnection(_ context.Context, domain, domainOwner, repository,
	externalConnection string) (*driver.Repository, error) {
	ref := driver.RepositoryRef{Domain: domain, Owner: domainOwner, Repository: repository}

	if err := validateRepositoryRef(ref); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	kept := make([]driver.ExternalConnection, 0, len(r.ExternalConnections))

	for _, ec := range r.ExternalConnections {
		if ec.ExternalConnectionName != externalConnection {
			kept = append(kept, ec)
		}
	}

	if len(kept) == len(r.ExternalConnections) {
		return nil, notFound(driver.ResourceTypeRepository, externalConnection,
			"Repository %s has no external connection %s", repository, externalConnection)
	}

	r.ExternalConnections = kept

	m.repos.Set(repoKey(domain, repository), r)

	out := copyRepository(&r)

	return &out, nil
}
