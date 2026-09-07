package codeartifact

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// CreateDomain provisions a new domain synchronously in the Active state with
// stable computed fields (arn, owner, createdTime and, when the caller supplied
// none, a generated encryption key). A domain name already in use yields a
// ConflictException.
func (m *Mock) CreateDomain(_ context.Context, in *driver.CreateDomainInput) (*driver.Domain, error) {
	if in.Name == "" {
		return nil, validation("domain name is required")
	}

	if m.domains.Has(in.Name) {
		return nil, conflict("Domain %s already exists", in.Name)
	}

	owner := m.opts.AccountID

	encryptionKey := in.EncryptionKey
	if encryptionKey == "" {
		encryptionKey = idgen.AWSARN("kms", m.opts.Region, owner, "key/"+idgen.UUID())
	}

	d := driver.Domain{
		Name:           in.Name,
		Owner:          owner,
		Arn:            m.domainARN(owner, in.Name),
		Status:         driver.DomainStatusActive,
		EncryptionKey:  encryptionKey,
		S3BucketArn:    idgen.ARN("aws", "s3", "", "", "assets-"+owner+"-"+m.opts.Region+"-"+idgen.UUID()),
		CreatedTime:    m.now(),
		AssetSizeBytes: 0,
		Tags:           copyTags(in.Tags),
	}

	m.domains.Set(in.Name, d)

	out := m.hydrateDomain(&d)

	return &out, nil
}

// DescribeDomain returns a copy of the domain. The stored computed fields are
// returned unchanged and repositoryCount is recomputed live.
func (m *Mock) DescribeDomain(_ context.Context, domain, _ string) (*driver.Domain, error) {
	d, ok := m.domains.Get(domain)
	if !ok {
		return nil, notFound("Domain %s not found", domain)
	}

	out := m.hydrateDomain(&d)

	return &out, nil
}

// DeleteDomain removes an empty domain and returns its description. A domain that
// still contains repositories yields a ConflictException, mirroring the real
// API which requires the domain to be empty first.
func (m *Mock) DeleteDomain(_ context.Context, domain, _ string) (*driver.Domain, error) {
	d, ok := m.domains.Get(domain)
	if !ok {
		return nil, notFound("Domain %s not found", domain)
	}

	if m.repositoryCount(domain) > 0 {
		return nil, conflict("Domain %s cannot be deleted because it still contains repositories", domain)
	}

	out := m.hydrateDomain(&d)
	out.Status = driver.DomainStatusDeleted

	m.domains.Delete(domain)

	return &out, nil
}

// ListDomains returns a deterministic page of domains ordered by name.
func (m *Mock) ListDomains(_ context.Context, page driver.Page) ([]*driver.Domain, string, error) {
	stored := m.domains.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Domain, 0, end-start)

	for i := start; i < end; i++ {
		d := m.hydrateDomain(&stored[i])
		out = append(out, &d)
	}

	return out, next, nil
}

// hydrateDomain returns an alias-free copy of a domain with its live-derived
// repositoryCount filled in.
func (m *Mock) hydrateDomain(d *driver.Domain) driver.Domain {
	out := copyDomain(d)
	out.RepositoryCount = m.repositoryCount(d.Name)

	return out
}
