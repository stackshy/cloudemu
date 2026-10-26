package codeartifact

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// CreateDomain provisions a new domain synchronously in the Active state with
// stable computed fields (arn, owner, createdTime and, when the caller supplied
// none, a generated encryption key). A domain name already in use yields a
// ConflictException; an account holds at most 10 domains.
func (m *Mock) CreateDomain(_ context.Context, in *driver.CreateDomainInput) (*driver.Domain, error) {
	if err := validateDomainName(in.Name); err != nil {
		return nil, err
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	if len(in.EncryptionKey) > maxEncryptionKey || hasSpace(in.EncryptionKey) {
		return nil, validation("encryptionKey must be 1-%d characters without whitespace", maxEncryptionKey)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.domains.Has(in.Name) {
		return nil, conflict(driver.ResourceTypeDomain, in.Name, "Domain %s already exists", in.Name)
	}

	if m.domains.Len() >= maxDomains {
		return nil, quotaExceeded(driver.ResourceTypeDomain, in.Name, "An account can have at most %d domains", maxDomains)
	}

	owner := m.opts.AccountID

	encryptionKey := in.EncryptionKey
	if encryptionKey == "" {
		encryptionKey = idgen.AWSARN("kms", m.opts.Region, owner, "key/"+idgen.UUID())
	}

	d := driver.Domain{
		Name:          in.Name,
		Owner:         owner,
		Arn:           m.domainARN(owner, in.Name),
		Status:        driver.DomainStatusActive,
		EncryptionKey: encryptionKey,
		S3BucketArn:   idgen.ARN("aws", "s3", "", "", "assets-"+owner+"-"+m.opts.Region+"-"+idgen.UUID()),
		CreatedTime:   m.now(),
		Tags:          copyTags(in.Tags),
	}

	m.domains.Set(in.Name, d)

	out := m.hydrateDomain(&d)

	return &out, nil
}

// DescribeDomain returns a copy of the domain. The stored computed fields are
// returned unchanged; repositoryCount and assetSizeBytes are recomputed live.
func (m *Mock) DescribeDomain(_ context.Context, domain, domainOwner string) (*driver.Domain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, err := m.domainLocked(driver.DomainRef{Domain: domain, Owner: domainOwner})
	if err != nil {
		return nil, err
	}

	out := m.hydrateDomain(&d)

	return &out, nil
}

// DeleteDomain removes an empty domain together with its policy and package
// groups, and returns its description. A domain that still contains
// repositories yields a ConflictException, mirroring the real API which
// requires the domain to be empty first.
func (m *Mock) DeleteDomain(_ context.Context, domain, domainOwner string) (*driver.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, err := m.domainLocked(driver.DomainRef{Domain: domain, Owner: domainOwner})
	if err != nil {
		return nil, err
	}

	if m.repositoryCountLocked(domain) > 0 {
		return nil, conflict(driver.ResourceTypeDomain, domain,
			"Domain %s cannot be deleted because it still contains repositories", domain)
	}

	out := m.hydrateDomain(&d)
	out.Status = driver.DomainStatusDeleted

	m.domains.Delete(domain)
	m.domainPolicies.Delete(domain)

	for key := range m.groups.Filter(func(k string, _ *groupRecord) bool { return strings.HasPrefix(k, domain+"|") }) {
		m.groups.Delete(key)
	}

	return &out, nil
}

// ListDomains returns a deterministic page of domains ordered by name.
func (m *Mock) ListDomains(_ context.Context, page driver.Page) ([]*driver.Domain, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stored := m.domains.SortedValues()

	start, end, next, err := paginate(len(stored), page)
	if err != nil {
		return nil, "", err
	}

	out := make([]*driver.Domain, 0, end-start)

	for i := start; i < end; i++ {
		d := m.hydrateDomain(&stored[i])
		out = append(out, &d)
	}

	return out, next, nil
}

// hydrateDomain returns an alias-free copy of a domain with its live-derived
// repositoryCount and assetSizeBytes filled in. The caller holds mu.
func (m *Mock) hydrateDomain(d *driver.Domain) driver.Domain {
	out := copyDomain(d)
	out.RepositoryCount = m.repositoryCountLocked(d.Name)
	out.AssetSizeBytes = m.domainAssetBytesLocked(d.Name)

	return out
}

// repositoryCountLocked returns the number of repositories in a domain.
func (m *Mock) repositoryCountLocked(domain string) int64 {
	return int64(len(m.repos.Filter(func(_ string, r driver.Repository) bool { return r.DomainName == domain })))
}
