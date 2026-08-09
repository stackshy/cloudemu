package acm_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newACMClient(t *testing.T) *awsacm.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{ACM: cloud.ACM})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsacm.NewFromConfig(cfg, func(o *awsacm.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKRequestDescribeGetCertificate(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:              aws.String("example.com"),
		SubjectAlternativeNames: []string{"www.example.com"},
		ValidationMethod:        acmtypes.ValidationMethodDns,
		Tags:                    []acmtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("RequestCertificate: %v", err)
	}

	arn := aws.ToString(req.CertificateArn)
	if !strings.Contains(arn, ":acm:") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	desc, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}

	cd := desc.Certificate
	if aws.ToString(cd.DomainName) != "example.com" || cd.Status != acmtypes.CertificateStatusIssued {
		t.Fatalf("unexpected certificate: domain=%s status=%s", aws.ToString(cd.DomainName), cd.Status)
	}

	if cd.Type != acmtypes.CertificateTypeAmazonIssued {
		t.Fatalf("type = %s, want AMAZON_ISSUED", cd.Type)
	}

	if len(cd.DomainValidationOptions) == 0 || cd.DomainValidationOptions[0].ResourceRecord == nil {
		t.Fatalf("expected DNS validation record: %+v", cd.DomainValidationOptions)
	}

	// GetCertificate returns real, parseable PEM.
	got, err := c.GetCertificate(ctx, &awsacm.GetCertificateInput{CertificateArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	block, _ := pem.Decode([]byte(aws.ToString(got.Certificate)))
	if block == nil {
		t.Fatal("GetCertificate did not return valid PEM")
	}

	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("returned certificate does not parse: %v", err)
	}

	if parsed.Subject.CommonName != "example.com" {
		t.Fatalf("cert CN = %s, want example.com", parsed.Subject.CommonName)
	}
}

func TestSDKListAndDelete(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	for _, d := range []string{"a.com", "b.com", "c.com"} {
		if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String(d)}); err != nil {
			t.Fatalf("request %s: %v", d, err)
		}
	}

	list, err := c.ListCertificates(ctx, &awsacm.ListCertificatesInput{})
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}

	if len(list.CertificateSummaryList) != 3 {
		t.Fatalf("want 3 certs, got %d", len(list.CertificateSummaryList))
	}

	arn := aws.ToString(list.CertificateSummaryList[0].CertificateArn)
	if _, err := c.DeleteCertificate(ctx, &awsacm.DeleteCertificateInput{CertificateArn: aws.String(arn)}); err != nil {
		t.Fatalf("DeleteCertificate: %v", err)
	}

	list, _ = c.ListCertificates(ctx, &awsacm.ListCertificatesInput{})
	if len(list.CertificateSummaryList) != 2 {
		t.Fatalf("after delete want 2, got %d", len(list.CertificateSummaryList))
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("tag.com")})
	arn := req.CertificateArn

	if _, err := c.AddTagsToCertificate(ctx, &awsacm.AddTagsToCertificateInput{
		CertificateArn: arn, Tags: []acmtypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("AddTagsToCertificate: %v", err)
	}

	tags, err := c.ListTagsForCertificate(ctx, &awsacm.ListTagsForCertificateInput{CertificateArn: arn})
	if err != nil {
		t.Fatalf("ListTagsForCertificate: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}

	if _, err := c.RemoveTagsFromCertificate(ctx, &awsacm.RemoveTagsFromCertificateInput{
		CertificateArn: arn, Tags: []acmtypes.Tag{{Key: aws.String("team")}},
	}); err != nil {
		t.Fatalf("RemoveTagsFromCertificate: %v", err)
	}
}

func TestSDKDescribeMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	_, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{
		CertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/missing"),
	})
	if err == nil {
		t.Fatal("expected error for missing certificate")
	}

	var nf *acmtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ResourceNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ResourceNotFoundException, got %v", err)
	}
}
