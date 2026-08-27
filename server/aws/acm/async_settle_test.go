package acm_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAsyncSettleWireACM pins that a real SDK client sees a DNS-validated
// certificate as PENDING_VALIDATION (with its CNAME record) until the settle
// window elapses, then ISSUED, and that ListCertificates filtered by
// PENDING_VALIDATION follows the transition — all over the wire.
func TestAsyncSettleWireACM(t *testing.T) {
	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{ACM: cloud.ACM}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	c := awsacm.NewFromConfig(cfg, func(o *awsacm.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName: aws.String("example.com"), ValidationMethod: acmtypes.ValidationMethodDns,
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	desc, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}
	if desc.Certificate.Status != acmtypes.CertificateStatusPendingValidation {
		t.Fatalf("status = %q, want PENDING_VALIDATION", desc.Certificate.Status)
	}
	if len(desc.Certificate.DomainValidationOptions) == 0 ||
		desc.Certificate.DomainValidationOptions[0].ResourceRecord == nil {
		t.Fatal("expected a DNS validation ResourceRecord (CNAME) while pending")
	}

	list, err := c.ListCertificates(ctx, &awsacm.ListCertificatesInput{
		CertificateStatuses: []acmtypes.CertificateStatus{acmtypes.CertificateStatusPendingValidation},
	})
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(list.CertificateSummaryList) != 1 {
		t.Fatalf("PENDING_VALIDATION filter returned %d, want 1", len(list.CertificateSummaryList))
	}

	fc.Advance(3 * time.Second) // past DefaultCertificateSettle (2s)

	desc, _ = c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	if desc.Certificate.Status != acmtypes.CertificateStatusIssued {
		t.Fatalf("status after settle = %q, want ISSUED", desc.Certificate.Status)
	}
	list, _ = c.ListCertificates(ctx, &awsacm.ListCertificatesInput{
		CertificateStatuses: []acmtypes.CertificateStatus{acmtypes.CertificateStatusPendingValidation},
	})
	if len(list.CertificateSummaryList) != 0 {
		t.Fatalf("PENDING_VALIDATION filter after settle = %d, want 0", len(list.CertificateSummaryList))
	}
}
