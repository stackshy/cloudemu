package acm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

func TestSDKImportAndExport(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	// Generate real cert material by requesting + exporting a managed cert,
	// then import it as a new IMPORTED certificate.
	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("src.com")})
	exp, err := c.ExportCertificate(ctx, &awsacm.ExportCertificateInput{
		CertificateArn: req.CertificateArn, Passphrase: []byte("hunter2"),
	})
	if err != nil {
		t.Fatalf("ExportCertificate: %v", err)
	}

	if aws.ToString(exp.Certificate) == "" || aws.ToString(exp.PrivateKey) == "" {
		t.Fatalf("export missing material: %+v", exp)
	}

	imp, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte(aws.ToString(exp.Certificate)),
		PrivateKey:  []byte(aws.ToString(exp.PrivateKey)),
	})
	if err != nil {
		t.Fatalf("ImportCertificate: %v", err)
	}

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: imp.CertificateArn})
	if desc.Certificate.Type != acmtypes.CertificateTypeImported {
		t.Fatalf("imported cert type = %s, want IMPORTED", desc.Certificate.Type)
	}

	if aws.ToString(desc.Certificate.DomainName) != "src.com" {
		t.Fatalf("imported cert domain = %s, want src.com", aws.ToString(desc.Certificate.DomainName))
	}

	// Importing garbage is rejected.
	if _, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte("not a cert"), PrivateKey: []byte("nope"),
	}); err == nil {
		t.Fatal("importing invalid PEM should fail")
	}
}

func TestSDKRenewChangesSerial(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("renew.com")})
	arn := req.CertificateArn

	before, _ := c.GetCertificate(ctx, &awsacm.GetCertificateInput{CertificateArn: arn})

	if _, err := c.RenewCertificate(ctx, &awsacm.RenewCertificateInput{CertificateArn: arn}); err != nil {
		t.Fatalf("RenewCertificate: %v", err)
	}

	after, _ := c.GetCertificate(ctx, &awsacm.GetCertificateInput{CertificateArn: arn})
	if bytes.Equal([]byte(aws.ToString(before.Certificate)), []byte(aws.ToString(after.Certificate))) {
		t.Fatal("renew should re-issue a new certificate")
	}
}

func TestSDKUpdateOptionsAndRevoke(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("opt.com")})
	arn := req.CertificateArn

	if _, err := c.UpdateCertificateOptions(ctx, &awsacm.UpdateCertificateOptionsInput{
		CertificateArn: arn,
		Options:        &acmtypes.CertificateOptions{CertificateTransparencyLoggingPreference: acmtypes.CertificateTransparencyLoggingPreferenceDisabled},
	}); err != nil {
		t.Fatalf("UpdateCertificateOptions: %v", err)
	}

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: arn})
	if desc.Certificate.Options.CertificateTransparencyLoggingPreference != acmtypes.CertificateTransparencyLoggingPreferenceDisabled {
		t.Fatalf("CT logging pref not updated: %+v", desc.Certificate.Options)
	}

	if _, err := c.RevokeCertificate(ctx, &awsacm.RevokeCertificateInput{
		CertificateArn: arn, RevocationReason: acmtypes.RevocationReasonKeyCompromise,
	}); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	desc, _ = c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: arn})
	if desc.Certificate.Status != acmtypes.CertificateStatusRevoked {
		t.Fatalf("status = %s, want REVOKED", desc.Certificate.Status)
	}
}

func TestSDKAccountConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	if _, err := c.PutAccountConfiguration(ctx, &awsacm.PutAccountConfigurationInput{
		ExpiryEvents:     &acmtypes.ExpiryEventsConfiguration{DaysBeforeExpiry: aws.Int32(21)},
		IdempotencyToken: aws.String("tok-1"),
	}); err != nil {
		t.Fatalf("PutAccountConfiguration: %v", err)
	}

	got, err := c.GetAccountConfiguration(ctx, &awsacm.GetAccountConfigurationInput{})
	if err != nil {
		t.Fatalf("GetAccountConfiguration: %v", err)
	}

	if got.ExpiryEvents == nil || aws.ToInt32(got.ExpiryEvents.DaysBeforeExpiry) != 21 {
		t.Fatalf("unexpected account config: %+v", got.ExpiryEvents)
	}
}
