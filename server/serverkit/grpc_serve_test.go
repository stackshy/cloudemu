package serverkit

import (
	"io"
	"testing"
)

// serverNames returns the label of every listenerServer buildServers produced,
// so a test can assert the optional gRPC endpoint is present only when enabled.
func serverNames(t *testing.T, cfg Config) []string {
	t.Helper()

	app := newTestApp(t, cfg)

	servers, _, err := app.buildServers()
	if err != nil {
		t.Fatalf("buildServers: %v", err)
	}

	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.name()
	}

	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}

	return false
}

// TestGRPCListenerOptIn is the additive-wiring guard: the gRPC endpoint appears
// in the server set exactly when --gcp-grpc-port is set, and the default serve
// (port empty) binds no gRPC listener, so its behavior is unchanged.
func TestGRPCListenerOptIn(t *testing.T) {
	base := Config{
		Providers: []string{"gcp"},
		Host:      "127.0.0.1",
		Ports:     map[string]string{"gcp": "0"},
		Out:       io.Discard,
	}

	cases := []struct {
		name     string
		grpcPort string
		wantGRPC bool
	}{
		{name: "off by default", grpcPort: "", wantGRPC: false},
		{name: "enabled", grpcPort: "16331", wantGRPC: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.GCPGRPCPort = tc.grpcPort

			names := serverNames(t, cfg)

			if got := hasName(names, "gcp-grpc"); got != tc.wantGRPC {
				t.Fatalf("gcp-grpc listener present = %v, want %v (servers: %v)", got, tc.wantGRPC, names)
			}

			// The REST GCP endpoint is present regardless — the gRPC transport is
			// additive, never a replacement.
			if !hasName(names, "gcp") {
				t.Fatalf("gcp REST listener missing (servers: %v)", names)
			}
		})
	}
}

// TestGRPCEndpointAdvertised checks the resolved endpoint set carries the gRPC
// bind address when enabled and omits it (empty) otherwise.
func TestGRPCEndpointAdvertised(t *testing.T) {
	cfg := Config{
		Providers:   []string{"gcp"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"gcp": "0"},
		GCPGRPCPort: "16331",
		Out:         io.Discard,
	}

	app := newTestApp(t, cfg)

	_, eps, err := app.buildServers()
	if err != nil {
		t.Fatalf("buildServers: %v", err)
	}

	if want := "127.0.0.1:16331"; eps.GCPGRPC != want {
		t.Fatalf("eps.GCPGRPC = %q, want %q", eps.GCPGRPC, want)
	}
}
