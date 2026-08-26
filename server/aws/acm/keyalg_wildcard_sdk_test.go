package acm_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

// F1 — RequestCertificate honors the documented EC/RSA KeyAlgorithm set.
func TestSDKRequestECKeyAlgorithm(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:   aws.String("ec.example.com"),
		KeyAlgorithm: acmtypes.KeyAlgorithmEcPrime256v1,
	})
	if err != nil {
		t.Fatalf("RequestCertificate(EC_prime256v1): %v", err)
	}

	desc, err := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	if err != nil {
		t.Fatalf("DescribeCertificate: %v", err)
	}

	if desc.Certificate.KeyAlgorithm != acmtypes.KeyAlgorithmEcPrime256v1 {
		t.Fatalf("KeyAlgorithm = %s, want EC_prime256v1", desc.Certificate.KeyAlgorithm)
	}

	if sig := aws.ToString(desc.Certificate.SignatureAlgorithm); !strings.HasPrefix(sig, "ECDSA") {
		t.Fatalf("SignatureAlgorithm = %q, want an ECDSA signature", sig)
	}

	// The issued material must actually carry an EC public key.
	got, err := c.GetCertificate(ctx, &awsacm.GetCertificateInput{CertificateArn: req.CertificateArn})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	block, _ := pem.Decode([]byte(aws.ToString(got.Certificate)))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}

	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Fatalf("issued cert public key = %T, want *ecdsa.PublicKey", leaf.PublicKey)
	}
}

func TestSDKRequestRSA4096(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:   aws.String("rsa4096.example.com"),
		KeyAlgorithm: acmtypes.KeyAlgorithmRsa4096,
	})
	if err != nil {
		t.Fatalf("RequestCertificate(RSA_4096): %v", err)
	}

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	if desc.Certificate.KeyAlgorithm != acmtypes.KeyAlgorithmRsa4096 {
		t.Fatalf("KeyAlgorithm = %s, want RSA_4096", desc.Certificate.KeyAlgorithm)
	}
}

func TestSDKRequestBogusKeyAlgorithmRejected(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	_, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:   aws.String("bogus.example.com"),
		KeyAlgorithm: acmtypes.KeyAlgorithm("RSA_9999"),
	})
	if err == nil {
		t.Fatal("an unsupported KeyAlgorithm should be rejected")
	}

	assertAPIErrorCode(t, err, "InvalidParameterException")
}

// F2 — wildcard DNS validation record is rooted at the base domain (no literal
// '*') and a wildcard + its apex share a single validation record.
func TestSDKWildcardValidationRecordRooted(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:       aws.String("*.example.com"),
		ValidationMethod: acmtypes.ValidationMethodDns,
	})
	if err != nil {
		t.Fatalf("RequestCertificate(*.example.com): %v", err)
	}

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	dvos := desc.Certificate.DomainValidationOptions

	if len(dvos) != 1 || dvos[0].ResourceRecord == nil {
		t.Fatalf("want 1 DVO with a ResourceRecord, got %+v", dvos)
	}

	name := aws.ToString(dvos[0].ResourceRecord.Name)
	if strings.Contains(name, "*") {
		t.Fatalf("validation record name contains a literal '*': %q", name)
	}

	if !strings.HasSuffix(name, ".example.com.") {
		t.Fatalf("validation record name = %q, want it rooted at example.com", name)
	}

	value := aws.ToString(dvos[0].ResourceRecord.Value)
	if strings.Contains(value, "*") {
		t.Fatalf("validation record value contains a literal '*': %q", value)
	}
}

func TestSDKWildcardApexShareOneRecord(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:              aws.String("*.example.com"),
		SubjectAlternativeNames: []string{"example.com"},
		ValidationMethod:        acmtypes.ValidationMethodDns,
	})
	if err != nil {
		t.Fatalf("RequestCertificate wildcard+apex: %v", err)
	}

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	dvos := desc.Certificate.DomainValidationOptions

	if len(dvos) != 1 {
		t.Fatalf("wildcard + apex should share one validation record, got %d DVOs", len(dvos))
	}

	if name := aws.ToString(dvos[0].ResourceRecord.Name); !strings.HasSuffix(name, ".example.com.") {
		t.Fatalf("shared record name = %q, want rooted at example.com", name)
	}
}

func TestSDKNonWildcardValidationRecordUnchanged(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	req, _ := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:       aws.String("plain.example.com"),
		ValidationMethod: acmtypes.ValidationMethodDns,
	})

	desc, _ := c.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{CertificateArn: req.CertificateArn})
	dvos := desc.Certificate.DomainValidationOptions

	if len(dvos) != 1 || dvos[0].ResourceRecord == nil {
		t.Fatalf("want 1 DVO with a ResourceRecord, got %+v", dvos)
	}

	if name := aws.ToString(dvos[0].ResourceRecord.Name); name != "_acm-validations.plain.example.com." {
		t.Fatalf("non-wildcard record name = %q, want _acm-validations.plain.example.com.", name)
	}
}

// F4 — RequestCertificate validates the DomainName (and each SAN) as an FQDN.
func TestSDKRequestMalformedDomainRejected(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName: aws.String("not a valid domain!!"),
	}); err == nil {
		t.Fatal("a malformed DomainName should be rejected")
	} else {
		assertAPIErrorCode(t, err, "InvalidParameterException")
	}

	// A malformed SAN is rejected too.
	if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:              aws.String("good.example.com"),
		SubjectAlternativeNames: []string{"bad san!"},
	}); err == nil {
		t.Fatal("a malformed SAN should be rejected")
	}

	// A valid FQDN and a wildcard are accepted.
	if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName:              aws.String("good.example.com"),
		SubjectAlternativeNames: []string{"*.good.example.com"},
	}); err != nil {
		t.Fatalf("valid FQDN + wildcard should be accepted: %v", err)
	}
}

// F5 — default ListCertificates returns only RSA_2048; Includes.keyTypes widens
// it, and MaxItems/NextToken paginate.
func TestSDKListDefaultRSAFilterAndIncludes(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	// A default RSA_2048 managed cert.
	if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
		DomainName: aws.String("rsa.example.com"),
	}); err != nil {
		t.Fatalf("request RSA cert: %v", err)
	}

	// An imported EC certificate.
	certPEM, keyPEM := makeECCertPEM(t, "ec-import.example.com")
	if _, err := c.ImportCertificate(ctx, &awsacm.ImportCertificateInput{
		Certificate: []byte(certPEM), PrivateKey: []byte(keyPEM),
	}); err != nil {
		t.Fatalf("ImportCertificate(EC): %v", err)
	}

	// Default list excludes the EC cert.
	def, _ := c.ListCertificates(ctx, &awsacm.ListCertificatesInput{})
	for _, s := range def.CertificateSummaryList {
		if s.KeyAlgorithm == acmtypes.KeyAlgorithmEcPrime256v1 {
			t.Fatal("default ListCertificates should not return EC certs")
		}
	}

	if len(def.CertificateSummaryList) != 1 {
		t.Fatalf("default list want 1 RSA cert, got %d", len(def.CertificateSummaryList))
	}

	// Includes.keyTypes=[EC_prime256v1] surfaces the EC cert.
	inc, _ := c.ListCertificates(ctx, &awsacm.ListCertificatesInput{
		Includes: &acmtypes.Filters{KeyTypes: []acmtypes.KeyAlgorithm{acmtypes.KeyAlgorithmEcPrime256v1}},
	})
	if len(inc.CertificateSummaryList) != 1 ||
		inc.CertificateSummaryList[0].KeyAlgorithm != acmtypes.KeyAlgorithmEcPrime256v1 {
		t.Fatalf("Includes.keyTypes=[EC] should return the EC cert, got %+v", inc.CertificateSummaryList)
	}
}

func TestSDKListPagination(t *testing.T) {
	ctx := context.Background()
	c := newACMClient(t)

	const total = 5
	for i := 0; i < total; i++ {
		if _, err := c.RequestCertificate(ctx, &awsacm.RequestCertificateInput{
			DomainName: aws.String(fmt.Sprintf("p%d.example.com", i)),
		}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	var token *string
	pages := 0

	for {
		out, err := c.ListCertificates(ctx, &awsacm.ListCertificatesInput{
			MaxItems: aws.Int32(2), NextToken: token,
		})
		if err != nil {
			t.Fatalf("ListCertificates page: %v", err)
		}

		if len(out.CertificateSummaryList) > 2 {
			t.Fatalf("page returned %d items, want <= MaxItems (2)", len(out.CertificateSummaryList))
		}

		for _, s := range out.CertificateSummaryList {
			seen[aws.ToString(s.CertificateArn)] = true
		}

		pages++

		if out.NextToken == nil {
			break
		}

		token = out.NextToken

		if pages > total+2 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != total {
		t.Fatalf("paginated set = %d certs, want %d", len(seen), total)
	}

	if pages < 2 {
		t.Fatalf("MaxItems=2 over %d certs should span multiple pages, got %d", total, pages)
	}
}

// makeECCertPEM generates a real self-signed EC (P-256) cert + key as PEM.
func makeECCertPEM(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
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
		t.Fatalf("create EC cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	return certPEM, keyPEM
}
