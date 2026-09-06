package cognito

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// domainVersion is the version label Cognito stamps on a hosted-UI domain.
const domainVersion = "20200310"

// CreateUserPoolDomain binds a hosted-UI domain to a user pool, settling it to
// ACTIVE with a synthetic S3 bucket and CloudFront distribution.
func (m *Mock) CreateUserPoolDomain(_ context.Context, in driver.CreateUserPoolDomainInput) error {
	if in.Domain == "" {
		return invalidParameter("Domain is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.userPools.Has(in.UserPoolID) {
		return resourceNotFound("User pool %s does not exist.", in.UserPoolID)
	}

	if m.domains.Has(in.Domain) {
		return invalidParameter("Domain already associated with another user pool.")
	}

	domain := driver.UserPoolDomain{
		Domain:                 in.Domain,
		UserPoolID:             in.UserPoolID,
		AWSAccountID:           m.opts.AccountID,
		S3Bucket:               "aws-cognito-prod-" + m.opts.Region + "-assets",
		CloudFrontDistribution: fmt.Sprintf("%s.cloudfront.net", newClientID()),
		Version:                domainVersion,
		Status:                 driver.DomainStatusActive,
	}

	m.domains.Set(in.Domain, copyUserPoolDomain(domain))

	return nil
}

// DescribeUserPoolDomain returns a domain's description. An unknown domain yields
// an empty (zero) description and no error, matching real Cognito.
func (m *Mock) DescribeUserPoolDomain(_ context.Context, domain string) (*driver.UserPoolDomain, error) {
	d, ok := m.domains.Get(domain)
	if !ok {
		return &driver.UserPoolDomain{}, nil
	}

	out := copyUserPoolDomain(d)

	return &out, nil
}

// DeleteUserPoolDomain removes a hosted-UI domain.
func (m *Mock) DeleteUserPoolDomain(_ context.Context, domain, _ string) error {
	m.domains.Delete(domain)

	return nil
}
