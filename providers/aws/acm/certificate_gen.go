package acm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

const (
	rsaKeyBits    = 2048
	rsaBits1024   = 1024
	rsaBits4096   = 4096
	certValidDays = 395 // ACM public certs are valid ~13 months
	serialBits    = 128
)

// issuedMaterial is the generated PEM material for a certificate.
type issuedMaterial struct {
	certPEM  string
	keyPEM   string
	chainPEM string
	serial   string
	subject  string
	issuer   string
	notAfter time.Time
}

// generateCertificate issues a real self-signed X.509 certificate for the given
// domains, valid from now. The single self-signed cert doubles as its own chain
// root, which is enough for a local emulator (clients that pin a CA can add it).
func generateCertificate(domain string, sans []string, notBefore time.Time) (*issuedMaterial, error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, errors.Newf(errors.Internal, "serial: %v", err)
	}

	dnsNames := dedupeDomains(domain, sans)
	subject := pkix.Name{CommonName: domain}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		Issuer:                subject, // self-signed
		NotBefore:             notBefore,
		NotAfter:              notBefore.AddDate(0, 0, certValidDays),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "create certificate: %v", err)
	}

	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))

	return &issuedMaterial{
		certPEM:  certPEM,
		keyPEM:   keyPEM,
		chainPEM: certPEM, // self-signed: the cert is its own chain
		serial:   formatSerial(serial),
		subject:  "CN=" + domain,
		issuer:   "Amazon",
		notAfter: tmpl.NotAfter,
	}, nil
}

// formatSerial renders a serial number as colon-separated hex, matching ACM.
func formatSerial(n *big.Int) string {
	b := n.Bytes()
	if len(b) == 0 {
		return "00"
	}

	parts := make([]string, 0, len(b))
	for _, by := range b {
		parts = append(parts, fmt.Sprintf("%02x", by))
	}

	return strings.Join(parts, ":")
}

func dedupeDomains(domain string, sans []string) []string {
	seen := map[string]bool{domain: true}
	out := []string{domain}

	for _, s := range sans {
		if seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	return out
}

// parseCertificatePEM validates that a PEM blob contains a parseable
// certificate and returns its leaf, for ImportCertificate.
func parseCertificatePEM(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, invalidParameter("Certificate is not valid PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, invalidParameter("Certificate could not be parsed: %v", err)
	}

	return cert, nil
}

// pemBytes is a small helper to pass PEM strings to crypto/tls.
func pemBytes(s string) []byte { return []byte(s) }

// keyAlgorithmOf reports the ACM KeyAlgorithm string matching a certificate's
// public key, so an imported cert's Describe reflects its real key.
func keyAlgorithmOf(leaf *x509.Certificate) string {
	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		switch pub.N.BitLen() {
		case rsaBits1024:
			return driver.KeyAlgRSA1024
		case rsaBits4096:
			return driver.KeyAlgRSA4096
		default:
			return driver.KeyAlgRSA2048
		}
	case *ecdsa.PublicKey:
		if pub.Curve == elliptic.P384() {
			return driver.KeyAlgECP384
		}

		return driver.KeyAlgECP256
	default:
		return driver.KeyAlgRSA2048
	}
}

// signatureAlgorithmOf maps a parsed certificate's signature algorithm to the
// ACM SignatureAlgorithm string.
func signatureAlgorithmOf(leaf *x509.Certificate) string {
	//nolint:exhaustive // the default arm maps every other algorithm to the RSA-SHA256 baseline
	switch leaf.SignatureAlgorithm {
	case x509.SHA384WithRSA:
		return "SHA384WITHRSA"
	case x509.SHA512WithRSA:
		return "SHA512WITHRSA"
	case x509.ECDSAWithSHA256:
		return "ECDSAWITHSHA256"
	case x509.ECDSAWithSHA384:
		return "ECDSAWITHSHA384"
	default:
		return "SHA256WITHRSA"
	}
}
