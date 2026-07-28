package eks

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// advertisedCAPool builds the trust store a caller assembles from a cluster
// description: base64 PEM in, CertPool out. Anything client-go can do with
// the advertised CA, it can do with this.
func advertisedCAPool(t *testing.T) *x509.CertPool {
	t.Helper()

	advertised := stubCertificate()
	if advertised == "" {
		t.Fatal("cluster advertises an empty certificate authority")
	}

	raw, err := base64.StdEncoding.DecodeString(advertised)
	if err != nil {
		t.Fatalf("advertised CA is not valid base64: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatal("advertised CA is not a usable PEM certificate")
	}

	return pool
}

// TestServingTLSConfig_ChainsToAdvertisedCA is the end-to-end version of the
// claim the advertised CA makes: a client that trusts *only* that CA must
// complete a real handshake against the data plane, with no
// InsecureSkipVerify anywhere. If the serving leaf ever stops being signed by
// the CA clusters advertise, this fails.
func TestServingTLSConfig_ChainsToAdvertisedCA(t *testing.T) {
	t.Parallel()

	tlsCfg, err := ServingTLSConfig([]string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("ServingTLSConfig: %v", err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
	srv.TLS = tlsCfg
	srv.StartTLS()

	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    advertisedCAPool(t),
		},
	}}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("handshake against the advertised CA failed: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
}

// TestServingTLSConfig_LeafCoversRequestedHosts checks the SANs, since a
// chain that verifies but omits the address the caller dials still fails the
// handshake with a hostname error.
func TestServingTLSConfig_LeafCoversRequestedHosts(t *testing.T) {
	t.Parallel()

	tlsCfg, err := ServingTLSConfig([]string{"127.0.0.1", "kubernetes.default"})
	if err != nil {
		t.Fatalf("ServingTLSConfig: %v", err)
	}

	leaf, err := x509.ParseCertificate(tlsCfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse serving leaf: %v", err)
	}

	opts := x509.VerifyOptions{
		Roots:     advertisedCAPool(t),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("serving leaf does not chain to the advertised CA: %v", err)
	}

	if err := leaf.VerifyHostname("kubernetes.default"); err != nil {
		t.Errorf("leaf is missing the requested DNS SAN: %v", err)
	}

	var hasLoopback bool

	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			hasLoopback = true
			break
		}
	}

	if !hasLoopback {
		t.Error("leaf is missing the requested IP SAN 127.0.0.1")
	}
}

// TestServingTLSConfig_RejectsUntrustedRoot guards against the test above
// passing for the wrong reason — an empty or permissive pool would verify
// anything.
func TestServingTLSConfig_RejectsUntrustedRoot(t *testing.T) {
	t.Parallel()

	tlsCfg, err := ServingTLSConfig([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("ServingTLSConfig: %v", err)
	}

	leaf, err := x509.ParseCertificate(tlsCfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse serving leaf: %v", err)
	}

	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     x509.NewCertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Error("leaf verified against an empty root pool — the check is vacuous")
	}
}
