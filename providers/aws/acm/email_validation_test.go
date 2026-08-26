package acm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws/acm"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func onlyDV(t *testing.T, m *acm.Mock, arn string) driver.DomainValidation {
	t.Helper()

	cert, err := m.DescribeCertificate(context.Background(), arn)
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}

	if len(cert.DomainValidationOptions) != 1 {
		t.Fatalf("want 1 DomainValidation, got %d", len(cert.DomainValidationOptions))
	}

	return cert.DomainValidationOptions[0]
}

func assertEmails(t *testing.T, got []string, domain string) {
	t.Helper()

	want := []string{
		"admin@" + domain, "administrator@" + domain, "hostmaster@" + domain,
		"postmaster@" + domain, "webmaster@" + domain,
	}
	if len(got) != len(want) {
		t.Fatalf("ValidationEmails = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ValidationEmails[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestEmailValidationEmits pins that an EMAIL-validated certificate exposes the
// five well-known approver mailboxes and no DNS ResourceRecord.
func TestEmailValidationEmits(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName: "example.com", ValidationMethod: driver.ValidationEmail,
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	dv := onlyDV(t, m, arn)
	if dv.ValidationMethod != driver.ValidationEmail {
		t.Fatalf("ValidationMethod = %q, want EMAIL", dv.ValidationMethod)
	}
	if dv.ValidationDomain != "example.com" {
		t.Fatalf("ValidationDomain = %q, want example.com", dv.ValidationDomain)
	}
	if dv.ResourceRecordN != "" || dv.ResourceRecordT != "" || dv.ResourceRecordV != "" {
		t.Fatalf("EMAIL cert must not carry a DNS ResourceRecord, got %+v", dv)
	}

	assertEmails(t, dv.ValidationEmails, "example.com")
}

// TestEmailValidationWildcardRoot pins that a wildcard domain roots its approver
// mailboxes at the base domain (never a literal "*").
func TestEmailValidationWildcardRoot(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName: "*.example.com", ValidationMethod: driver.ValidationEmail,
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	dv := onlyDV(t, m, arn)
	if dv.ValidationDomain != "example.com" {
		t.Fatalf("ValidationDomain = %q, want example.com", dv.ValidationDomain)
	}

	assertEmails(t, dv.ValidationEmails, "example.com")
}

// TestEmailValidationHonorsValidationDomain pins that an explicit ValidationDomain
// in DomainValidationOptions routes the approver mailboxes to that superdomain.
func TestEmailValidationHonorsValidationDomain(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName:       "www.example.com",
		ValidationMethod: driver.ValidationEmail,
		DomainValidationOptions: []driver.DomainValidationOption{
			{DomainName: "www.example.com", ValidationDomain: "example.com"},
		},
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	dv := onlyDV(t, m, arn)
	if dv.ValidationDomain != "example.com" {
		t.Fatalf("ValidationDomain = %q, want example.com", dv.ValidationDomain)
	}

	assertEmails(t, dv.ValidationEmails, "example.com")
}

// TestDNSValidationUnchanged guards that the default (DNS) path still exposes a
// CNAME ResourceRecord and never a ValidationEmails list.
func TestDNSValidationUnchanged(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{DomainName: "example.com"})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	dv := onlyDV(t, m, arn)
	if dv.ValidationMethod != driver.ValidationDNS {
		t.Fatalf("default ValidationMethod = %q, want DNS", dv.ValidationMethod)
	}
	if dv.ResourceRecordN == "" || dv.ResourceRecordT != "CNAME" {
		t.Fatalf("DNS cert must carry a CNAME ResourceRecord, got %+v", dv)
	}
	if len(dv.ValidationEmails) != 0 {
		t.Fatalf("DNS cert must not carry ValidationEmails, got %v", dv.ValidationEmails)
	}
	if dv.ValidationDomain != "example.com" {
		t.Fatalf("ValidationDomain = %q, want example.com", dv.ValidationDomain)
	}
}
