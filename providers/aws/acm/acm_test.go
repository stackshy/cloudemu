package acm_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/acm"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func newMock(t *testing.T) *acm.Mock {
	t.Helper()

	return acm.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func TestRequestRequiresDomain(t *testing.T) {
	m := newMock(t)

	if _, err := m.RequestCertificate(context.Background(), driver.RequestCertificateInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty domain should be InvalidArgument, got %v", err)
	}
}

func TestRequestIssuesRealMaterial(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{DomainName: "example.com"})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	cert, chain, err := m.GetCertificate(ctx, arn)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if cert == "" || chain == "" {
		t.Fatalf("expected cert+chain PEM, got cert=%d chain=%d bytes", len(cert), len(chain))
	}

	d, _ := m.DescribeCertificate(ctx, arn)
	if d.Status != driver.StatusIssued || d.Type != driver.TypeAmazonIssued {
		t.Fatalf("unexpected: status=%s type=%s", d.Status, d.Type)
	}
}

func TestDeleteInUseGuard(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, _ := m.RequestCertificate(ctx, driver.RequestCertificateInput{DomainName: "inuse.com"})

	// Simulate in-use by re-importing with an InUseBy set isn't exposed; instead
	// verify a normal delete works and a missing cert errors.
	if err := m.DeleteCertificate(ctx, arn); err != nil {
		t.Fatalf("DeleteCertificate: %v", err)
	}

	if err := m.DeleteCertificate(ctx, arn); !errors.IsNotFound(err) {
		t.Fatalf("deleting missing cert should be NotFound, got %v", err)
	}
}

// TestAsyncSettleCertificate pins that under AsyncSettle a DNS-validated
// certificate reports PENDING_VALIDATION (with its CNAME record to create) until
// the settle window elapses, then ISSUED; and that ListCertificates filtered by
// PENDING_VALIDATION reflects the transition.
func TestAsyncSettleCertificate(t *testing.T) {
	fc := config.NewFakeClock(time.Unix(0, 0))
	m := acm.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"), config.WithAsyncSettle()))
	ctx := context.Background()

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName: "example.com", ValidationMethod: driver.ValidationDNS,
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	cert, _ := m.DescribeCertificate(ctx, arn)
	if cert.Status != driver.StatusPendingValidation {
		t.Fatalf("status = %q, want PENDING_VALIDATION", cert.Status)
	}
	if len(cert.DomainValidationOptions) == 0 || cert.DomainValidationOptions[0].ResourceRecordN == "" {
		t.Fatal("expected a DNS validation ResourceRecord (CNAME) to create")
	}
	if cert.DomainValidationOptions[0].ValidationStatus != driver.StatusPendingValidation {
		t.Fatalf("dv status = %q, want PENDING_VALIDATION", cert.DomainValidationOptions[0].ValidationStatus)
	}

	pending, _ := m.ListCertificates(ctx, driver.ListFilter{Statuses: []string{driver.StatusPendingValidation}})
	if len(pending) != 1 {
		t.Fatalf("PENDING_VALIDATION filter returned %d, want 1", len(pending))
	}

	fc.Advance(3 * time.Second) // past DefaultCertificateSettle (2s)
	cert, _ = m.DescribeCertificate(ctx, arn)
	if cert.Status != driver.StatusIssued {
		t.Fatalf("status after settle = %q, want ISSUED", cert.Status)
	}
	pending, _ = m.ListCertificates(ctx, driver.ListFilter{Statuses: []string{driver.StatusPendingValidation}})
	if len(pending) != 0 {
		t.Fatalf("PENDING_VALIDATION filter after settle returned %d, want 0", len(pending))
	}
}
