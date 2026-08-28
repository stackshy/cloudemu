package serverkit

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// TestTLSConfigSelfSigned checks the generated cert certifies localhost, the
// loopback IPs, the bind host, and any extra --tls-host SANs.
func TestTLSConfigSelfSigned(t *testing.T) {
	cfg := Config{Host: "myhost", TLSHosts: []string{"1.2.3.4", "extra.local"}}

	tc, err := tlsConfig(&cfg, "myhost:4568")
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if len(tc.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(tc.Certificates))
	}

	leaf, err := x509.ParseCertificate(tc.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	wantDNS := map[string]bool{"localhost": false, "myhost": false, "extra.local": false}
	for _, n := range leaf.DNSNames {
		if _, ok := wantDNS[n]; ok {
			wantDNS[n] = true
		}
	}
	for name, seen := range wantDNS {
		if !seen {
			t.Fatalf("cert DNS names %v missing %q", leaf.DNSNames, name)
		}
	}

	var haveLoopback, haveExtra bool
	for _, ip := range leaf.IPAddresses {
		if ip.String() == "127.0.0.1" {
			haveLoopback = true
		}
		if ip.String() == "1.2.3.4" {
			haveExtra = true
		}
	}
	if !haveLoopback {
		t.Fatalf("cert IPs %v missing loopback 127.0.0.1", leaf.IPAddresses)
	}
	if !haveExtra {
		t.Fatalf("cert IPs %v missing extra SAN 1.2.3.4", leaf.IPAddresses)
	}
}

// TestTLSConfigCustomCert checks a user-supplied --tls-cert/--tls-key pair is
// loaded instead of minting a self-signed cert.
func TestTLSConfigCustomCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	cert, err := selfSignedCert([]string{"localhost"})
	if err != nil {
		t.Fatalf("mint cert for fixture: %v", err)
	}
	writePEM(t, certPath, "CERTIFICATE", cert.Certificate[0])
	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey)) //nolint:forcetypeassert // selfSignedCert uses ECDSA
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)

	cfg := Config{TLSCert: certPath, TLSKey: keyPath}
	tc, err := tlsConfig(&cfg, "127.0.0.1:4568")
	if err != nil {
		t.Fatalf("tlsConfig with custom cert: %v", err)
	}
	if len(tc.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(tc.Certificates))
	}
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	b := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
