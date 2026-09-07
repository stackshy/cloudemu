package codeartifact

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// CreateRepository provisions a new repository in an existing domain with stable
// computed fields (arn, administratorAccount, domainOwner and createdTime). The
// parent domain must exist or a ResourceNotFoundException is returned; a
// repository name already used in the domain yields a ConflictException.
func (m *Mock) CreateRepository(_ context.Context, in *driver.CreateRepositoryInput) (*driver.Repository, error) {
	if in.Repository == "" {
		return nil, validation("repository name is required")
	}

	owner := m.resolveOwner(in.DomainOwner)

	if !m.domains.Has(in.Domain) {
		return nil, notFound("Domain %s not found", in.Domain)
	}

	key := repoKey(in.Domain, in.Repository)
	if m.repos.Has(key) {
		return nil, conflict("Repository %s already exists in domain %s", in.Repository, in.Domain)
	}

	r := driver.Repository{
		Name:                 in.Repository,
		AdministratorAccount: owner,
		DomainName:           in.Domain,
		DomainOwner:          owner,
		Arn:                  m.repositoryARN(owner, in.Domain, in.Repository),
		Description:          in.Description,
		CreatedTime:          m.now(),
		Upstreams:            copyUpstreams(in.Upstreams),
		Tags:                 copyTags(in.Tags),
	}

	m.repos.Set(key, r)

	out := copyRepository(&r)

	return &out, nil
}

// DescribeRepository returns a copy of the repository. Stored computed fields are
// returned unchanged so repeated reads never drift.
func (m *Mock) DescribeRepository(_ context.Context, domain, _, repository string) (*driver.Repository, error) {
	r, ok := m.repos.Get(repoKey(domain, repository))
	if !ok {
		return nil, notFound("Repository %s not found in domain %s", repository, domain)
	}

	out := copyRepository(&r)

	return &out, nil
}

// UpdateRepository replaces the description and/or upstreams the request
// supplied, leaving unmentioned fields untouched. The computed identity fields
// are preserved.
func (m *Mock) UpdateRepository(_ context.Context, in *driver.UpdateRepositoryInput) (*driver.Repository, error) {
	var updated driver.Repository

	ok := m.repos.Update(repoKey(in.Domain, in.Repository), func(r driver.Repository) driver.Repository {
		if in.Description != nil {
			r.Description = *in.Description
		}

		if in.Upstreams != nil {
			r.Upstreams = copyUpstreams(*in.Upstreams)
		}

		updated = r

		return r
	})
	if !ok {
		return nil, notFound("Repository %s not found in domain %s", in.Repository, in.Domain)
	}

	out := copyRepository(&updated)

	return &out, nil
}

// DeleteRepository removes a repository and returns its description.
func (m *Mock) DeleteRepository(_ context.Context, domain, _, repository string) (*driver.Repository, error) {
	key := repoKey(domain, repository)

	r, ok := m.repos.Get(key)
	if !ok {
		return nil, notFound("Repository %s not found in domain %s", repository, domain)
	}

	out := copyRepository(&r)

	m.repos.Delete(key)

	return &out, nil
}

// ListRepositories returns a deterministic page of all repositories ordered by
// their store key, optionally filtered by a name prefix.
func (m *Mock) ListRepositories(_ context.Context, prefix string,
	page driver.Page) ([]*driver.Repository, string, error) {
	return m.listRepos(prefix, page, func(driver.Repository) bool { return true })
}

// ListRepositoriesInDomain returns a deterministic page of the repositories in a
// single domain, optionally filtered by a name prefix.
func (m *Mock) ListRepositoriesInDomain(_ context.Context, domain, _, prefix string,
	page driver.Page) ([]*driver.Repository, string, error) {
	return m.listRepos(prefix, page, func(r driver.Repository) bool { return r.DomainName == domain })
}

// listRepos is the shared paginated repository lister.
func (m *Mock) listRepos(prefix string, page driver.Page,
	keep func(driver.Repository) bool) ([]*driver.Repository, string, error) {
	all := m.repos.SortedValues()
	filtered := make([]driver.Repository, 0, len(all))

	for i := range all {
		if keep(all[i]) && strings.HasPrefix(all[i].Name, prefix) {
			filtered = append(filtered, all[i])
		}
	}

	start, end, next := paginate(len(filtered), page)
	out := make([]*driver.Repository, 0, end-start)

	for i := start; i < end; i++ {
		r := copyRepository(&filtered[i])
		out = append(out, &r)
	}

	return out, next, nil
}

// AssociateExternalConnection adds an external connection to a repository. Only
// one external connection is allowed, so a second association yields a
// ConflictException.
func (m *Mock) AssociateExternalConnection(_ context.Context, domain, _, repository,
	externalConnection string) (*driver.Repository, error) {
	var updated driver.Repository

	found := m.repos.Update(repoKey(domain, repository), func(r driver.Repository) driver.Repository {
		r.ExternalConnections = []driver.ExternalConnection{{
			ExternalConnectionName: externalConnection,
			PackageFormat:          packageFormatFor(externalConnection),
			Status:                 driver.ExternalConnStatusAvailable,
		}}
		updated = r

		return r
	})
	if !found {
		return nil, notFound("Repository %s not found in domain %s", repository, domain)
	}

	out := copyRepository(&updated)

	return &out, nil
}

// DisassociateExternalConnection removes the named external connection from a
// repository.
func (m *Mock) DisassociateExternalConnection(_ context.Context, domain, _, repository,
	externalConnection string) (*driver.Repository, error) {
	var updated driver.Repository

	found := m.repos.Update(repoKey(domain, repository), func(r driver.Repository) driver.Repository {
		kept := make([]driver.ExternalConnection, 0, len(r.ExternalConnections))

		for _, ec := range r.ExternalConnections {
			if ec.ExternalConnectionName != externalConnection {
				kept = append(kept, ec)
			}
		}

		r.ExternalConnections = kept
		updated = r

		return r
	})
	if !found {
		return nil, notFound("Repository %s not found in domain %s", repository, domain)
	}

	out := copyRepository(&updated)

	return &out, nil
}

// packageFormatFor derives the package format an external connection serves from
// its name (for example public:npmjs -> npm).
func packageFormatFor(externalConnection string) string {
	name := externalConnection
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[idx+1:]
	}

	switch {
	case strings.HasPrefix(name, "npm"):
		return "npm"
	case strings.HasPrefix(name, "pypi"):
		return "pypi"
	case strings.HasPrefix(name, "maven"):
		return "maven"
	case strings.HasPrefix(name, "nuget"):
		return "nuget"
	case strings.HasPrefix(name, "ruby"):
		return "ruby"
	case strings.HasPrefix(name, "crates"):
		return "cargo"
	default:
		return ""
	}
}
