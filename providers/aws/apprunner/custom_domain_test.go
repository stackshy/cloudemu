package apprunner_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func TestCustomDomains(t *testing.T) {
	m := newMock()
	svc := mustNamed(t, m, "domain-app")
	other := mustNamed(t, m, "other-app")

	res, err := m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireNoError(t, err)

	if !res.CustomDomain.EnableWWWSubdomain || res.CustomDomain.Status != driver.DomainStatusPendingCertificateDNSValidate ||
		res.DNSTarget != svc.ServiceURL || len(res.CustomDomain.CertificateValidationRecords) != 2 {
		t.Fatalf("unexpected association: %+v %+v", res, res.CustomDomain)
	}

	for _, r := range res.CustomDomain.CertificateValidationRecords {
		if r.Type != "CNAME" || r.Status != "PENDING_VALIDATION" || !strings.HasSuffix(r.Value, ".acm-validations.aws.") {
			t.Fatalf("unexpected validation record %+v", r)
		}
	}

	// The same domain cannot go to the same or another service.
	_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireInvalidRequest(t, err)

	_, err = m.AssociateCustomDomain(bg, other.ServiceArn, "example.com", nil)
	requireInvalidRequest(t, err)

	for _, bad := range []string{"", "bad domain", "under_score.com", strings.Repeat("a", 256)} {
		_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, bad, nil)
		requireInvalidRequest(t, err)
	}

	_, err = m.AssociateCustomDomain(bg, "arn:aws:apprunner:us-east-1:123456789012:service/ghost/abc", "x.com", nil)
	assertException(t, err, driver.ExResourceNotFound)

	noWWW := false
	res, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "*.example.org", &noWWW)
	requireNoError(t, err)

	if res.CustomDomain.EnableWWWSubdomain || len(res.CustomDomain.CertificateValidationRecords) != 1 {
		t.Fatalf("EnableWWWSubdomain=false must not add a www record: %+v", res.CustomDomain)
	}

	desc, next, err := m.DescribeCustomDomains(bg, svc.ServiceArn, driver.Page{})
	requireNoError(t, err)

	if len(desc.CustomDomains) != 2 || next != "" || desc.CustomDomains[0].DomainName != "*.example.org" {
		t.Fatalf("unexpected describe: %+v", desc.CustomDomains)
	}

	page, next, _ := m.DescribeCustomDomains(bg, svc.ServiceArn, driver.Page{MaxResults: 1})
	if len(page.CustomDomains) != 1 || next == "" {
		t.Fatalf("paging: %+v next=%q", page.CustomDomains, next)
	}

	gone, err := m.DisassociateCustomDomain(bg, svc.ServiceArn, "example.com")
	requireNoError(t, err)
	assertStr(t, gone.CustomDomain.Status, driver.DomainStatusDeleting)

	_, err = m.DisassociateCustomDomain(bg, svc.ServiceArn, "example.com")
	requireInvalidRequest(t, err)

	// The freed domain can move to the other service; deleting a service frees its domains.
	_, err = m.AssociateCustomDomain(bg, other.ServiceArn, "example.com", nil)
	requireNoError(t, err)

	_, err = m.DeleteService(bg, other.ServiceArn)
	requireNoError(t, err)

	_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireNoError(t, err)
}

func TestCustomDomainReportsVpcDNSTargets(t *testing.T) {
	m := newMock()

	res, err := m.CreateService(bg, privateService("dns-private"))
	requireNoError(t, err)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-dns", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-7", VpcEndpointID: "vpce-7"},
	})
	requireNoError(t, err)

	assoc, err := m.AssociateCustomDomain(bg, res.Service.ServiceArn, "private.example.com", nil)
	requireNoError(t, err)

	if len(assoc.VpcDNSTargets) != 1 || assoc.VpcDNSTargets[0].VpcID != "vpc-7" || assoc.VpcDNSTargets[0].VpcIngressConnectionArn != conn.VpcIngressConnectionArn {
		t.Fatalf("unexpected VPC DNS targets: %+v", assoc.VpcDNSTargets)
	}
}

func TestCustomDomainAsyncStatus(t *testing.T) {
	m, clk := newAsyncMock()

	res, err := m.CreateService(bg, namedService("async-domain"))
	requireNoError(t, err)
	clk.Advance(time.Minute)

	assoc, err := m.AssociateCustomDomain(bg, res.Service.ServiceArn, "async.example.com", nil)
	requireNoError(t, err)
	assertStr(t, assoc.CustomDomain.Status, driver.DomainStatusCreating)

	clk.Advance(time.Minute)

	desc, _, _ := m.DescribeCustomDomains(bg, res.Service.ServiceArn, driver.Page{})
	assertStr(t, desc.CustomDomains[0].Status, driver.DomainStatusPendingCertificateDNSValidate)
}
