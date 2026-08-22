package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

const (
	// serialBits is the bit width of the random certificate serial number.
	serialBits = 128
	// certValidYears is how long the generated self-signed cert stays valid.
	certValidYears = 1
	// certBackdate keeps NotBefore slightly in the past to tolerate clock skew.
	certBackdate = time.Hour
)

// selfSignedTLSConfig returns a TLS config for the Azure HTTPS endpoint using an
// in-memory self-signed certificate covering localhost, the loopback IPs, and
// the bind host. It is a local-development convenience — clients must trust the
// cert or skip verification.
func selfSignedTLSConfig(host string) (*tls.Config, error) {
	cert, err := selfSignedCert([]string{"localhost", host})
	if err != nil {
		return nil, err
	}

	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

// selfSignedCert generates an in-memory self-signed certificate valid for the
// given hosts (DNS names and/or IP literals).
func selfSignedCert(hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"cloudemu local"}},
		NotBefore:             time.Now().Add(-certBackdate),
		NotAfter:              time.Now().AddDate(certValidYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	// Always cover the loopback IPs so https://127.0.0.1 / [::1] verify.
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"), net.IPv6loopback)

	addSANs(&tmpl, hosts)

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("assemble keypair: %w", err)
	}

	return cert, nil
}

// addSANs adds each host to the template as either an IP or a DNS SAN, skipping
// blanks and duplicates.
func addSANs(tmpl *x509.Certificate, hosts []string) {
	seen := map[string]bool{}

	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}

		seen[h] = true

		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
}
