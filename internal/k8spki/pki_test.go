package k8spki

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServingTLSConfig_PinsHTTP11 asserts the k8s data-plane serving config
// offers only http/1.1 via ALPN. This is load-bearing: pods/exec and pods/attach
// hijack the connection for a WebSocket upgrade (golang.org/x/net/websocket calls
// http.Hijacker.Hijack with no fallback), which PANICS under an HTTP/2 connection.
// http/1.1 must be first (and h2 absent) so the listener never negotiates h2.
func TestServingTLSConfig_PinsHTTP11(t *testing.T) {
	cfg, err := ServingTLSConfig([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("ServingTLSConfig: %v", err)
	}

	if len(cfg.NextProtos) == 0 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatalf("NextProtos = %v, want http/1.1 first", cfg.NextProtos)
	}

	for _, p := range cfg.NextProtos {
		if p == "h2" {
			t.Fatalf("NextProtos %v must not offer h2 (Hijack panics under h2)", cfg.NextProtos)
		}
	}
}

// TestServingTLSConfig_NegotiatesHTTP11UnderH2Offer is the panic-path proof: a
// client offering h2 first in ALPN must still negotiate http/1.1 against the k8s
// listener, so the WebSocket hijack path is never reached over an h2 connection.
func TestServingTLSConfig_NegotiatesHTTP11UnderH2Offer(t *testing.T) {
	cfg, err := ServingTLSConfig([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("ServingTLSConfig: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		TLSConfig:         cfg,
		ReadHeaderTimeout: 5 * time.Second,
	}
	// ServeTLS is where net/http would auto-enable h2 for a listener whose
	// TLSConfig does not pin NextProtos; running it exercises that path.
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	defer srv.Close()

	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test dial; we only inspect the negotiated ALPN.
		NextProtos:         []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()

	if got := conn.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("negotiated ALPN = %q, want http/1.1 (h2 would panic the WebSocket hijack)", got)
	}
}
