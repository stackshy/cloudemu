// Package k8spki holds the single certificate authority the emulated
// Kubernetes data plane is served with and that every managed-Kubernetes
// control plane (EKS, AKS, GKE) advertises to clients.
//
// A cluster's advertised CA has to certify the data plane it points at, or it
// is decoration: a client building a rest.Config from the cluster's endpoint +
// CA would present that CA to a server whose serving certificate it did not
// sign, and the TLS handshake would fail. Keeping one CA here — used by both
// the serving TLS config and all three providers' advertised CA — makes the
// three connect paths validate identically.
package k8spki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

const (
	rsaKeyBits   = 2048
	caValidYears = 10
	serialBits   = 128
)

// loadCA generates the CA once and reuses it. The private key is retained (not
// discarded) so ServingTLSConfig can mint leaf certificates the advertised CA
// certifies. A generation failure is carried rather than swallowed: a silently
// empty CA is exactly the placeholder-that-breaks-client-go failure this
// package exists to prevent.
//
//nolint:gochecknoglobals // process-wide sync.OnceValues cache for the single CA.
var loadCA = sync.OnceValues(initCA)

type caMaterial struct {
	pem  string
	cert *x509.Certificate
	key  *rsa.PrivateKey
}

func initCA() (caMaterial, error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return caMaterial{}, fmt.Errorf("generate CA key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cloudemu-k8s-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(caValidYears, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return caMaterial{}, fmt.Errorf("create CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return caMaterial{}, fmt.Errorf("parse CA certificate: %w", err)
	}

	return caMaterial{
		key:  key,
		cert: cert,
		pem: base64.StdEncoding.EncodeToString(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}, nil
}

// CertificatePEM returns the base64-encoded PEM of the CA certificate a cluster
// advertises (EKS certificateAuthority.data, GKE masterAuth.clusterCaCertificate,
// AKS kubeconfig certificate-authority-data). Returns "" only if CA generation
// failed, which ServingTLSConfig surfaces as an error at serve time.
func CertificatePEM() string {
	ca, err := loadCA()
	if err != nil {
		return ""
	}

	return ca.pem
}

// ServingTLSConfig returns a TLS config for the Kubernetes data plane carrying a
// leaf signed by the advertised CA, with SANs for the given hosts. Serving the
// data plane with this is what makes the advertised CA true — a client can
// validate the endpoint against it.
func ServingTLSConfig(hosts []string) (*tls.Config, error) {
	ca, err := loadCA()
	if err != nil {
		return nil, fmt.Errorf("certificate authority unavailable: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generate serving key: %w", err)
	}

	// A distinct random serial per leaf: ServingTLSConfig is called once per
	// listener/test, each minting a fresh key, so a fixed serial would present
	// two different public keys under the same (issuer, serial) — which strict,
	// non-Go verifiers (the cross-SDK parity this package promises) can reject.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, fmt.Errorf("generate serving serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "cloudemu-k8s"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(caValidYears, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// Assert cA=FALSE explicitly so end-entity-strict verifiers accept the leaf.
		BasicConstraintsValid: true,
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)

			continue
		}

		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("sign serving certificate: %w", err)
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der, ca.cert.Raw},
			PrivateKey:  key,
		}},
	}, nil
}
