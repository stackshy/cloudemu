package acm_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	smithy "github.com/aws/smithy-go"
)

// assertAPIErrorCode fails unless err carries the given AWS API error code. Used
// for exceptions the SDK doesn't model on a given operation (so they arrive as
// generic API errors rather than typed structs).
func assertAPIErrorCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want API error %s, got %T: %v", want, err, err)
	}

	if apiErr.ErrorCode() != want {
		t.Fatalf("want API error code %s, got %s: %v", want, apiErr.ErrorCode(), err)
	}
}

// makeCertPEM generates a real self-signed cert + matching RSA key as PEM, so
// import tests use genuine, mutually-consistent material rather than depending
// on the server (real ACM won't export a managed cert's key).
func makeCertPEM(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{cn},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))

	return certPEM, keyPEM
}

func TestSDKImportAndExport(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	certPEM, keyPEM := makeCertPEM(t, "src.com")

	imp, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte(certPEM),
		PrivateKey:  []byte(keyPEM),
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

	// An imported cert is exportable (unlike a managed one).
	exp, err := c.ExportCertificate(ctx, &awsacm.ExportCertificateInput{
		CertificateArn: imp.CertificateArn, Passphrase: []byte("hunter2"),
	})
	if err != nil {
		t.Fatalf("ExportCertificate (imported): %v", err)
	}

	if aws.ToString(exp.Certificate) == "" || aws.ToString(exp.PrivateKey) == "" {
		t.Fatalf("export missing material: %+v", exp)
	}

	// Importing garbage is rejected.
	if _, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte("not a cert"), PrivateKey: []byte("nope"),
	}); err == nil {
		t.Fatal("importing invalid PEM should fail")
	}

	// A private key that doesn't match the certificate is rejected (M3).
	_, otherKey := makeCertPEM(t, "src.com")
	if _, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte(certPEM), PrivateKey: []byte(otherKey),
	}); err == nil {
		t.Fatal("importing a mismatched key should fail")
	}
}

// TestSDKExportManagedCertRejected verifies a public AMAZON_ISSUED certificate
// cannot have its private key exported (M1).
func TestSDKExportManagedCertRejected(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("managed.com")})

	_, err := c.ExportCertificate(ctx, &awsacm.ExportCertificateInput{
		CertificateArn: req.CertificateArn, Passphrase: []byte("hunter2"),
	})
	if err == nil {
		t.Fatal("exporting a managed AMAZON_ISSUED cert should fail")
	}

	// ExportCertificate's SDK model doesn't include InvalidStateException, so the
	// client surfaces it as a generic API error; assert on the wire error code.
	assertAPIErrorCode(t, err, "InvalidStateException")
}

// TestSDKRevokeNonIssuedRejected verifies revoking an already-revoked cert is
// rejected with InvalidStateException (M4).
func TestSDKRevokeNonIssuedRejected(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{DomainName: aws.String("revoke2.com")})
	arn := req.CertificateArn

	if _, err := c.RevokeCertificate(ctx, &awsacm.RevokeCertificateInput{
		CertificateArn: arn, RevocationReason: acmtypes.RevocationReasonKeyCompromise,
	}); err != nil {
		t.Fatalf("first RevokeCertificate: %v", err)
	}

	_, err := c.RevokeCertificate(ctx, &awsacm.RevokeCertificateInput{
		CertificateArn: arn, RevocationReason: acmtypes.RevocationReasonKeyCompromise,
	})
	if err == nil {
		t.Fatal("revoking an already-revoked cert should fail")
	}

	assertAPIErrorCode(t, err, "InvalidStateException")
}

// TestSDKInvalidArn verifies a malformed certificate ARN yields
// InvalidArnException rather than ResourceNotFoundException (M6).
func TestSDKInvalidArn(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	_, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{
		CertificateArn: aws.String("not-an-arn"),
	})
	if err == nil {
		t.Fatal("describing a malformed ARN should fail")
	}

	var iae *acmtypes.InvalidArnException
	if !errors.As(err, &iae) {
		t.Fatalf("want InvalidArnException, got %T: %v", err, err)
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
