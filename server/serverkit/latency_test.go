package serverkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLatencyDelaysProviderRequestsNotAdmin is the regression guard for the
// --latency no-op bug: with a latency configured, an emulated provider request
// must be delayed by at least that duration on the wire path, while an admin
// control-plane request (/_cloudemu/health) must NOT be — latency simulation is
// for the emulated cloud APIs, not the control plane.
func TestLatencyDelaysProviderRequestsNotAdmin(t *testing.T) {
	const latency = 40 * time.Millisecond

	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Admin:     true,
		Latency:   latency,
		Out:       io.Discard,
	})

	srv := httptest.NewServer(app.handlerFor(app.backends["aws"], app.seedFor("aws")))
	defer srv.Close()

	// A real round-trip to a provider endpoint must be delayed by >= latency.
	start := time.Now()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("provider request: %v", err)
	}
	_ = resp.Body.Close()

	if elapsed := time.Since(start); elapsed < latency {
		t.Fatalf("provider request took %v, want >= %v (latency not applied on the wire path)", elapsed, latency)
	}

	// The admin control plane must be answered without the latency delay.
	start = time.Now()
	resp, err = http.Get(srv.URL + "/_cloudemu/health")
	if err != nil {
		t.Fatalf("admin request: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin health status = %d, want 200", resp.StatusCode)
	}

	if elapsed := time.Since(start); elapsed >= latency {
		t.Fatalf("admin health took %v, want < %v (control plane must not be delayed)", elapsed, latency)
	}
}

// TestLatencyZeroAddsNoDelay asserts the middleware is a true no-op when latency
// is unset: the handler is returned unchanged, so there is zero overhead on the
// hot path.
func TestLatencyZeroAddsNoDelay(t *testing.T) {
	app := newTestApp(t, Config{
		Providers: []string{"aws"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"aws": "0"},
		Out:       io.Discard,
	})

	srv := httptest.NewServer(app.backends["aws"])
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("provider request: %v", err)
	}
	_ = resp.Body.Close()

	if elapsed := time.Since(start); elapsed >= 25*time.Millisecond {
		t.Fatalf("provider request took %v with latency=0; want no added delay", elapsed)
	}
}
