package apprunner

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// domainRecord is a custom domain stored under its service, keyed
// "serviceArn|domainName".
type domainRecord struct {
	ServiceArn string
	Domain     driver.CustomDomain
}

func domainKey(serviceArn, domain string) string { return serviceArn + "|" + domain }

func copyDomain(d *driver.CustomDomain) driver.CustomDomain {
	out := *d
	out.CertificateValidationRecords = append([]driver.CertificateValidationRecord(nil), d.CertificateValidationRecords...)

	return out
}

// viewDomain copies a domain with its status overlaid by any settle window:
// CREATING until the window elapses, then PENDING_CERTIFICATE_DNS_VALIDATION,
// the state a real association stays in until the customer adds the DNS records.
func (m *Mock) viewDomain(serviceArn string, d *driver.CustomDomain) driver.CustomDomain {
	out := copyDomain(d)
	out.Status = m.settling.State(domainKey(serviceArn, d.DomainName), m.opts.Clock.Now(), d.Status)

	return out
}

// validationRecords derives the CNAME records the customer adds to validate a
// domain (and its www subdomain). The emulator performs no DNS validation, so the
// records stay PENDING_VALIDATION.
func validationRecords(domain string, www bool) []driver.CertificateValidationRecord {
	names := []string{domain}
	if www {
		names = append(names, "www."+domain)
	}

	out := make([]driver.CertificateValidationRecord, len(names))

	for i, n := range names {
		token := newID()
		out[i] = driver.CertificateValidationRecord{
			Name: "_" + token[:16] + "." + n + ".", Type: "CNAME", Value: "_" + token[16:] + ".acm-validations.aws.",
			Status: "PENDING_VALIDATION",
		}
	}

	return out
}

// dnsTarget is the App Runner subdomain a custom domain is mapped to.
func dnsTarget(svc *driver.Service) string { return svc.ServiceURL }

// AssociateCustomDomain associates a domain (root, subdomain or wildcard) with a
// service. EnableWWWSubdomain defaults to true. The association starts CREATING
// and settles in PENDING_CERTIFICATE_DNS_VALIDATION; a domain that any service
// already has is rejected.
func (m *Mock) AssociateCustomDomain(
	_ context.Context, serviceArn, domainName string, enableWWW *bool,
) (*driver.CustomDomainResult, error) {
	if err := validateDomainName(domainName); err != nil {
		return nil, err
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	if err := m.requireIdle(&svc); err != nil {
		return nil, err
	}

	if m.domainTaken(domainName) {
		return nil, invalidRequest("the domain " + domainName + " is already associated with a service")
	}

	www := true
	if enableWWW != nil {
		www = *enableWWW
	}

	d := driver.CustomDomain{
		DomainName: domainName, EnableWWWSubdomain: www, Status: driver.DomainStatusPendingCertificateDNSValidate,
		CertificateValidationRecords: validationRecords(domainName, www),
	}

	m.domains.Set(domainKey(serviceArn, domainName), domainRecord{ServiceArn: serviceArn, Domain: d})
	m.settling.Begin(domainKey(serviceArn, domainName), driver.DomainStatusCreating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settleWindow))

	view := m.viewDomain(serviceArn, &d)

	return &driver.CustomDomainResult{
		ServiceArn: serviceArn, DNSTarget: dnsTarget(&svc), CustomDomain: &view, VpcDNSTargets: m.vpcDNSTargets(serviceArn),
	}, nil
}

func (m *Mock) domainTaken(domain string) bool {
	all := m.domains.SortedValues()
	for i := range all {
		if all[i].Domain.DomainName == domain {
			return true
		}
	}

	return false
}

// DisassociateCustomDomain removes a domain association and returns it with a
// DELETING status. A domain the service does not have is an InvalidRequestException.
func (m *Mock) DisassociateCustomDomain(
	_ context.Context, serviceArn, domainName string,
) (*driver.CustomDomainResult, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	rec, ok := m.domains.Get(domainKey(serviceArn, domainName))
	if !ok {
		return nil, invalidRequest("the domain " + domainName + " is not associated with the service")
	}

	m.domains.Delete(domainKey(serviceArn, domainName))
	m.settling.Clear(domainKey(serviceArn, domainName))

	out := copyDomain(&rec.Domain)
	out.Status = driver.DomainStatusDeleting

	return &driver.CustomDomainResult{
		ServiceArn: serviceArn, DNSTarget: dnsTarget(&svc), CustomDomain: &out, VpcDNSTargets: m.vpcDNSTargets(serviceArn),
	}, nil
}

// DescribeCustomDomains returns a page of a service's custom domains ordered by
// domain name, with the DNS target and the VPC DNS targets of its ingress
// connections.
func (m *Mock) DescribeCustomDomains(
	_ context.Context, serviceArn string, page driver.Page,
) (res *driver.CustomDomainResult, nextToken string, err error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, "", notFound("service %q does not exist", serviceArn)
	}

	var domains []driver.CustomDomain

	all := m.domains.SortedValues()
	for i := range all {
		if all[i].ServiceArn == serviceArn {
			domains = append(domains, m.viewDomain(serviceArn, &all[i].Domain))
		}
	}

	sort.Slice(domains, func(i, j int) bool { return domains[i].DomainName < domains[j].DomainName })

	start, end, next := paginate(len(domains), page)

	return &driver.CustomDomainResult{
		ServiceArn: serviceArn, DNSTarget: dnsTarget(&svc), CustomDomains: append([]driver.CustomDomain{}, domains[start:end]...),
		VpcDNSTargets: m.vpcDNSTargets(serviceArn),
	}, next, nil
}

// vpcDNSTargets lists the domain of each VPC ingress connection of a service.
func (m *Mock) vpcDNSTargets(serviceArn string) []driver.VpcDNSTarget {
	conns := m.ingressesOf(serviceArn)
	out := make([]driver.VpcDNSTarget, 0, len(conns))

	for i := range conns {
		out = append(out, driver.VpcDNSTarget{
			DomainName: conns[i].DomainName, VpcID: conns[i].IngressVpcConfiguration.VpcID,
			VpcIngressConnectionArn: conns[i].VpcIngressConnectionArn,
		})
	}

	return out
}

// cascadeService removes the custom domains and VPC ingress connections attached
// to a deleted service. The caller holds refMu.
func (m *Mock) cascadeService(serviceArn string) {
	for _, k := range m.domains.Keys() {
		if strings.HasPrefix(k, serviceArn+"|") {
			m.domains.Delete(k)
			m.settling.Clear(k)
		}
	}

	conns := m.ingressesOf(serviceArn)
	for i := range conns {
		m.ingress.Delete(conns[i].VpcIngressConnectionArn)
		m.settling.Clear(conns[i].VpcIngressConnectionArn)
	}
}
