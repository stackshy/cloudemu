package acm_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

// TestSDKEmailValidation pins that an EMAIL-validated certificate round-trips the
// approver mailbox list (and no DNS ResourceRecord) through the wire, honoring an
// explicit ValidationDomain from DomainValidationOptions.
func TestSDKEmailValidation(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:       aws.String("www.example.com"),
		ValidationMethod: acmtypes.ValidationMethodEmail,
		DomainValidationOptions: []acmtypes.DomainValidationOption{
			{DomainName: aws.String("www.example.com"), ValidationDomain: aws.String("example.com")},
		},
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	desc, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}

	dvos := desc.Certificate.DomainValidationOptions
	if len(dvos) != 1 {
		t.Fatalf("want 1 DomainValidation, got %d", len(dvos))
	}

	dv := dvos[0]
	if dv.ValidationMethod != acmtypes.ValidationMethodEmail {
		t.Fatalf("ValidationMethod = %s, want EMAIL", dv.ValidationMethod)
	}
	if aws.ToString(dv.ValidationDomain) != "example.com" {
		t.Fatalf("ValidationDomain = %q, want example.com", aws.ToString(dv.ValidationDomain))
	}
	if dv.ResourceRecord != nil {
		t.Fatalf("EMAIL cert must not carry a DNS ResourceRecord, got %+v", dv.ResourceRecord)
	}

	want := []string{
		"admin@example.com", "administrator@example.com", "hostmaster@example.com",
		"postmaster@example.com", "webmaster@example.com",
	}
	if len(dv.ValidationEmails) != len(want) {
		t.Fatalf("ValidationEmails = %v, want %v", dv.ValidationEmails, want)
	}

	for i := range want {
		if dv.ValidationEmails[i] != want[i] {
			t.Fatalf("ValidationEmails[%d] = %q, want %q", i, dv.ValidationEmails[i], want[i])
		}
	}
}
