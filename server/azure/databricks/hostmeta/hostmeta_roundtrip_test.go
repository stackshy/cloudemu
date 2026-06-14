package hostmeta_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/databricks/databricks-sdk-go/common/environment"
	"github.com/databricks/databricks-sdk-go/config"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/hostmeta"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := server.New()
	srv.Register(hostmeta.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// TestEndpointShape verifies the raw discovery document decodes into the exact
// struct databricks-sdk-go reads (config.HostMetadata).
func TestEndpointShape(t *testing.T) {
	ts := newServer(t)

	resp, err := http.Get(ts.URL + "/.well-known/databricks-config")
	if err != nil {
		t.Fatalf("GET discovery endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}

	var meta config.HostMetadata
	if err = json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode into config.HostMetadata: %v", err)
	}

	if meta.HostType != config.WorkspaceHost {
		t.Fatalf("got host_type %q, want %q", meta.HostType, config.WorkspaceHost)
	}

	if meta.Cloud != environment.CloudAzure {
		t.Fatalf("got cloud %q, want %q", meta.Cloud, environment.CloudAzure)
	}

	if meta.OIDCEndpoint == "" {
		t.Fatal("expected non-empty oidc_endpoint")
	}
}

// TestSDKResolvesMetadata drives the real SDK config resolver: with the
// endpoint served, EnsureResolved back-fills the cloud from host metadata
// instead of logging the resolution-failure warning.
func TestSDKResolvesMetadata(t *testing.T) {
	ts := newServer(t)

	cfg := &config.Config{
		Host:        ts.URL,
		Token:       "x",
		Credentials: config.PatCredentials{},
	}

	if err := cfg.EnsureResolved(); err != nil {
		t.Fatalf("EnsureResolved: %v", err)
	}

	if cfg.Cloud != environment.CloudAzure {
		t.Fatalf("got resolved cloud %q, want %q", cfg.Cloud, environment.CloudAzure)
	}
}

func TestNonGetDoesNotMatch(t *testing.T) {
	h := hostmeta.New()

	req := httptest.NewRequest(http.MethodPost, "/.well-known/databricks-config", nil)
	if h.Matches(req) {
		t.Fatal("POST should not match the discovery endpoint")
	}
}
