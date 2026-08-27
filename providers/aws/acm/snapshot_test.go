package acm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// TestSnapshotRoundTripACM proves a snapshot/restore round-trip preserves each
// certificate under its original ARN — including the issued PEM material — and
// the account-level configuration.
func TestSnapshotRoundTripACM(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	arn, err := src.RequestCertificate(ctx, driver.RequestCertificateInput{DomainName: "example.com"})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	if err := src.PutAccountConfiguration(ctx, driver.AccountConfiguration{DaysBeforeExpiry: 17}); err != nil {
		t.Fatalf("PutAccountConfiguration: %v", err)
	}

	wantCert, wantChain, err := src.GetCertificate(ctx, arn)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	d, err := dst.DescribeCertificate(ctx, arn)
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}

	if d.DomainName != "example.com" || d.Status != driver.StatusIssued {
		t.Fatalf("restored cert = %+v", d)
	}

	gotCert, gotChain, err := dst.GetCertificate(ctx, arn)
	if err != nil {
		t.Fatalf("GetCertificate after restore: %v", err)
	}

	if gotCert != wantCert || gotChain != wantChain {
		t.Fatalf("restored PEM material differs from original")
	}

	cfg, err := dst.GetAccountConfiguration(ctx)
	if err != nil || cfg.DaysBeforeExpiry != 17 {
		t.Fatalf("restored account config = %+v, err %v", cfg, err)
	}
}
