package serverkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enforceAuthApp(t *testing.T, mutate func(*Config)) *App {
	t.Helper()

	cfg := Config{
		Providers:   []string{"aws"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"aws": "0"},
		Admin:       true,
		EnforceAuth: true,
		Out:         io.Discard,
	}
	if mutate != nil {
		mutate(&cfg)
	}

	return newTestApp(t, cfg)
}

func adminDo(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestEnforceAuthAdminEndpointsRequireToken is the AUTHN-X3 guard: with
// --enforce-auth on, snapshot (GET and POST), reset and seed must refuse a
// caller without the admin token, so nobody can dump IAM secrets or replace
// the whole state anonymously.
func TestEnforceAuthAdminEndpointsRequireToken(t *testing.T) {
	app := enforceAuthApp(t, nil)
	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/_cloudemu/snapshot", ""},
		{http.MethodPost, "/_cloudemu/snapshot", `{"schemaVersion":1}`},
		{http.MethodPost, "/_cloudemu/reset", ""},
		{http.MethodPost, "/_cloudemu/seed", `{"buckets":[{"name":"b"}]}`},
		{http.MethodGet, "/_cloudemu/cost", ""},
		{http.MethodGet, "/_cloudemu/net/can-connect?from=a&to=b", ""},
		{http.MethodGet, "/_cloudemu/snapshot/", ""},
	}

	for _, tc := range cases {
		for _, token := range []string{"", "not-the-token"} {
			if rec := adminDo(h, tc.method, tc.path, token, tc.body); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s token=%q under --enforce-auth = %d, want 401", tc.method, tc.path, token, rec.Code)
			}
		}
	}

	if rec := adminDo(h, http.MethodGet, "/_cloudemu/health", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("health without a token = %d, want 200", rec.Code)
	}

	// The Kubernetes listener shares the same gate.
	k8s := app.handlerFor(app.backends["aws"], nil)
	if rec := adminDo(k8s, http.MethodGet, "/_cloudemu/snapshot", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("snapshot via a seedless listener = %d, want 401", rec.Code)
	}
}

func TestEnforceAuthAdminTokenAccepted(t *testing.T) {
	app := enforceAuthApp(t, func(c *Config) { c.AdminToken = "fixed-token" })
	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	rec := adminDo(h, http.MethodPost, "/_cloudemu/seed", "fixed-token", `{"buckets":[{"name":"seeded"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed with the token = %d %s", rec.Code, rec.Body.String())
	}

	rec = adminDo(h, http.MethodGet, "/_cloudemu/snapshot", "fixed-token", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "seeded") {
		t.Fatalf("snapshot with the token = %d, want 200 holding the seeded bucket", rec.Code)
	}

	snap := rec.Body.String()

	if rec = adminDo(h, http.MethodPost, "/_cloudemu/reset", "fixed-token", ""); rec.Code != http.StatusOK {
		t.Fatalf("reset with the token = %d", rec.Code)
	}

	if rec = adminDo(h, http.MethodPost, "/_cloudemu/snapshot", "fixed-token", snap); rec.Code != http.StatusOK {
		t.Fatalf("restore with the token = %d %s", rec.Code, rec.Body.String())
	}
}

func TestEnforceAuthGeneratedTokenWrittenToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-token")
	app := enforceAuthApp(t, func(c *Config) { c.AdminTokenFile = path })

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}

	token := strings.TrimSpace(string(raw))
	if len(token) < 32 {
		t.Fatalf("generated token %q is too short", token)
	}

	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))
	if rec := adminDo(h, http.MethodGet, "/_cloudemu/snapshot", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("snapshot with the generated token = %d", rec.Code)
	}

	// A second server gets a different token.
	path2 := filepath.Join(t.TempDir(), "admin-token")
	enforceAuthApp(t, func(c *Config) { c.AdminTokenFile = path2 })

	raw2, _ := os.ReadFile(path2)
	if strings.TrimSpace(string(raw2)) == token {
		t.Fatal("two servers generated the same admin token")
	}
}

func TestDangerWarningQuietUnderEnforceAuth(t *testing.T) {
	if w := dangerWarning(&Config{Admin: true, EnforceAuth: true, Host: "0.0.0.0"}); w != "" {
		t.Fatalf("token-gated admin on a public host: want no warning, got %q", w)
	}
}

// TestAuthOffAdminEndpointsUnchanged pins the documented developer default:
// without --enforce-auth the control plane stays open, even if a token is set.
func TestAuthOffAdminEndpointsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-token")
	app := newTestApp(t, Config{
		Providers:      []string{"aws"},
		Host:           "127.0.0.1",
		Ports:          map[string]string{"aws": "0"},
		Admin:          true,
		AdminToken:     "ignored",
		AdminTokenFile: path,
		Out:            io.Discard,
	})
	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	for _, p := range []string{"/_cloudemu/snapshot", "/_cloudemu/cost", "/_cloudemu/health"} {
		if rec := adminDo(h, http.MethodGet, p, "", ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s with auth off = %d, want 200", p, rec.Code)
		}
	}

	if rec := adminDo(h, http.MethodPost, "/_cloudemu/reset", "", ""); rec.Code != http.StatusOK {
		t.Errorf("reset with auth off = %d, want 200", rec.Code)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file written with auth off: %v", err)
	}
}

// TestSeedBootstrapsIAMUserUnderEnforceAuth covers the first-key bootstrap: the
// admin token seeds an IAM user with a known access key, which SigV4 then
// resolves.
func TestSeedBootstrapsIAMUserUnderEnforceAuth(t *testing.T) {
	app := enforceAuthApp(t, func(c *Config) { c.AdminToken = "tok" })
	h := app.handlerFor(app.backends["aws"], app.seedFor("aws"))

	fixture := `{"iamUsers":[{"name":"admin","accessKeys":[{"accessKeyId":"AKIABOOTSTRAP0000001","secretAccessKey":"boot-secret"}]}]}`
	if rec := adminDo(h, http.MethodPost, "/_cloudemu/seed", "tok", fixture); rec.Code != http.StatusOK {
		t.Fatalf("seed iam user = %d %s", rec.Code, rec.Body.String())
	}

	app.rebuildMu.Lock()
	base := app.awsMux.GetOrCreate("")
	app.rebuildMu.Unlock()

	ak, ok := base.IAM.AccessKeyByID(t.Context(), "AKIABOOTSTRAP0000001")
	if !ok || ak.SecretAccessKey != "boot-secret" || ak.UserName != "admin" {
		t.Fatalf("seeded key = %+v ok=%v", ak, ok)
	}
}
