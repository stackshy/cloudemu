package acm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func TestRequestECKeyAlgorithmReflected(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName:   "ec.example.com",
		KeyAlgorithm: driver.KeyAlgECP384,
	})
	if err != nil {
		t.Fatalf("RequestCertificate(EC_secp384r1): %v", err)
	}

	d, _ := m.DescribeCertificate(ctx, arn)
	if d.KeyAlgorithm != driver.KeyAlgECP384 {
		t.Fatalf("KeyAlgorithm = %s, want EC_secp384r1", d.KeyAlgorithm)
	}

	if d.SignatureAlgorithm != "ECDSAWITHSHA384" {
		t.Fatalf("SignatureAlgorithm = %s, want ECDSAWITHSHA384", d.SignatureAlgorithm)
	}
}

func TestRequestUnsupportedKeyAlgorithmRejected(t *testing.T) {
	m := newMock(t)

	_, err := m.RequestCertificate(context.Background(), driver.RequestCertificateInput{
		DomainName:   "bad.example.com",
		KeyAlgorithm: "RSA_9999",
	})
	if !errors.IsInvalidArgument(err) {
		t.Fatalf("unsupported KeyAlgorithm should be InvalidArgument, got %v", err)
	}
}

func TestWildcardValidationRecordRootedAndDeduped(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	arn, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName:              "*.example.com",
		SubjectAlternativeNames: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("RequestCertificate wildcard+apex: %v", err)
	}

	d, _ := m.DescribeCertificate(ctx, arn)
	if len(d.DomainValidationOptions) != 1 {
		t.Fatalf("wildcard + apex should collapse to 1 DVO, got %d", len(d.DomainValidationOptions))
	}

	rec := d.DomainValidationOptions[0].ResourceRecordN
	if strings.Contains(rec, "*") {
		t.Fatalf("validation record name contains '*': %q", rec)
	}

	if rec != "_acm-validations.example.com." {
		t.Fatalf("validation record name = %q, want _acm-validations.example.com.", rec)
	}
}

func TestRequestMalformedDomainRejected(t *testing.T) {
	m := newMock(t)

	for _, bad := range []string{"not a valid domain!!", "single", "a.b", "-lead.example.com"} {
		if _, err := m.RequestCertificate(context.Background(),
			driver.RequestCertificateInput{DomainName: bad}); !errors.IsInvalidArgument(err) {
			t.Fatalf("DomainName %q should be InvalidArgument, got %v", bad, err)
		}
	}

	// Valid FQDN + wildcard accepted.
	if _, err := m.RequestCertificate(context.Background(),
		driver.RequestCertificateInput{DomainName: "*.good.example.com"}); err != nil {
		t.Fatalf("valid wildcard should be accepted: %v", err)
	}
}

func TestListDefaultRSAFilterAndKeyTypes(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if _, err := m.RequestCertificate(ctx,
		driver.RequestCertificateInput{DomainName: "rsa.example.com"}); err != nil {
		t.Fatalf("request RSA: %v", err)
	}

	if _, err := m.RequestCertificate(ctx, driver.RequestCertificateInput{
		DomainName: "ec.example.com", KeyAlgorithm: driver.KeyAlgECP256,
	}); err != nil {
		t.Fatalf("request EC: %v", err)
	}

	def, _ := m.ListCertificates(ctx, driver.ListFilter{})
	if len(def) != 1 || def[0].KeyAlgorithm != driver.KeyAlgRSA2048 {
		t.Fatalf("default list want 1 RSA_2048 cert, got %+v", def)
	}

	ec, _ := m.ListCertificates(ctx, driver.ListFilter{KeyTypes: []string{driver.KeyAlgECP256}})
	if len(ec) != 1 || ec[0].KeyAlgorithm != driver.KeyAlgECP256 {
		t.Fatalf("KeyTypes=[EC] want 1 EC cert, got %+v", ec)
	}
}
