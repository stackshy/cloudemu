package eks

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

// The certificate authority a cluster advertises has to be able to certify the
// data plane that cluster points at, or it is decoration: a caller building a
// rest.Config from Endpoint plus CertificateAuthority would present it to a
// server whose certificate it did not sign.
//
// The CA private key is therefore retained rather than discarded, so
// ServingTLSConfig can mint a leaf for whoever serves the data plane.
var (
	caOnce sync.Once
	caPEM  string
	caCert *x509.Certificate
	caKey  *rsa.PrivateKey
)

func initCA() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cloudemu-eks-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return
	}

	caKey = key
	caCert = cert
	caPEM = base64.StdEncoding.EncodeToString(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// stubCertificate returns the base64 PEM a cluster advertises as its
// certificate authority.
func stubCertificate() string {
	caOnce.Do(initCA)

	return caPEM
}

// ServingTLSConfig returns a TLS configuration for the Kubernetes data plane,
// carrying a leaf signed by the CA clusters advertise.
//
// Serving the data plane with this makes the advertised CA true: a caller can
// validate the endpoint against it, which is what a rest.Config built from a
// cluster description does. Serving plain HTTP instead leaves the CA describing
// a TLS server that does not exist.
func ServingTLSConfig(hosts []string) (*tls.Config, error) {
	caOnce.Do(initCA)

	if caKey == nil || caCert == nil {
		return nil, fmt.Errorf("certificate authority unavailable")
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate serving key: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "cloudemu-k8s"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}

		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign serving certificate: %w", err)
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der, caCert.Raw},
			PrivateKey:  key,
		}},
	}, nil
}
