package apprunner

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// AssociateCustomDomain associates a custom domain with a service. The domain is
// stored under the resolved service's lock (children live with their parent),
// created PENDING_CERTIFICATE_DNS_VALIDATION and immediately settled ACTIVE with
// a synthesized validation record. AssociateCustomDomain does not model
// ResourceNotFoundException, so a missing service is an InvalidRequestException.
func (m *Mock) AssociateCustomDomain(
	_ context.Context, serviceArn, domainName string, enableWWW bool,
) (*driver.CustomDomain, string, error) {
	if domainName == "" {
		return nil, "", invalidRequest("DomainName is required")
	}

	sd, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, "", invalidRequest("no App Runner service found for ARN %q", serviceArn)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if _, exists := sd.domains[domainName]; exists {
		return nil, "", invalidRequest("custom domain %q is already associated", domainName)
	}

	cd := &driver.CustomDomain{
		DomainName: domainName, EnableWWWSubdomain: enableWWW,
		Status: driver.CustomDomainStatusActive, ServiceArn: serviceArn,
		CertificateValidationRecords: []driver.CertificateValidationRecord{{
			Name: "_" + idgen.GenerateID("") + "." + domainName, Type: "CNAME",
			Value:  "_" + idgen.GenerateID("") + ".acm-validations.aws.",
			Status: driver.CertValidationStatusSuccess,
		}},
	}
	sd.domains[domainName] = cd

	return copyCustomDomain(cd), sd.svc.ServiceURL, nil
}

// DisassociateCustomDomain removes a custom domain from a service, returning its
// final DELETING state.
func (m *Mock) DisassociateCustomDomain(
	_ context.Context, serviceArn, domainName string,
) (*driver.CustomDomain, string, error) {
	sd, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, "", invalidRequest("no App Runner service found for ARN %q", serviceArn)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	cd, ok := sd.domains[domainName]
	if !ok {
		return nil, "", invalidRequest("custom domain %q is not associated", domainName)
	}

	cd.Status = driver.CustomDomainStatusDeleting
	out := copyCustomDomain(cd)

	delete(sd.domains, domainName)

	return out, sd.svc.ServiceURL, nil
}

// DescribeCustomDomains lists a service's custom domains plus the service DNS
// target, paginated by domain name. It models ResourceNotFoundException for a
// missing service.
func (m *Mock) DescribeCustomDomains(
	_ context.Context, serviceArn, nextToken string, maxResults int32,
) (domains []driver.CustomDomain, dnsTarget, token string, err error) {
	sd, err := m.getService(serviceArn)
	if err != nil {
		return nil, "", "", err
	}

	sd.mu.RLock()
	dnsTarget = sd.svc.ServiceURL
	all := make([]driver.CustomDomain, 0, len(sd.domains))

	for _, cd := range sd.domains {
		all = append(all, *copyCustomDomain(cd))
	}
	sd.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].DomainName < all[j].DomainName })

	page, token, err := paginate(all, nextToken, maxResults, func(c driver.CustomDomain) string { return c.DomainName })

	return page, dnsTarget, token, err
}

// copyCustomDomain deep-copies a custom domain, including its validation records.
func copyCustomDomain(c *driver.CustomDomain) *driver.CustomDomain {
	out := *c

	if c.CertificateValidationRecords != nil {
		out.CertificateValidationRecords = append(
			[]driver.CertificateValidationRecord(nil), c.CertificateValidationRecords...)
	}

	return &out
}
