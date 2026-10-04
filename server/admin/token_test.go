package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/admin"
)

const testToken = "s3cret-admin-token"

// tokenControl builds a Control with every endpoint wired, so a 401 can only
// come from the token gate and a 200 proves the request reached the handler.
func tokenControl(token string) (c *admin.Control, resets *int) {
	n := 0
	b := admin.NewBackend(handler("backend"))
	extra := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	c = admin.NewControl(b,
		func() { n++ },
		func([]byte) (int, error) { return 1, nil },
		func() ([]byte, error) { return []byte(`{"secret":"AKIA"}`), nil },
		func([]byte) error { return nil },
		extra,
	)

	if token != "" {
		c.RequireToken(token)
	}

	return c, &n
}

var gatedEndpoints = []struct{ method, path string }{
	{http.MethodGet, admin.Prefix + "snapshot"},
	{http.MethodPost, admin.Prefix + "snapshot"},
	{http.MethodPost, admin.Prefix + "reset"},
	{http.MethodPost, admin.Prefix + "seed"},
	{http.MethodGet, admin.Prefix + "cost"},
	{http.MethodGet, admin.Prefix + "net/can-connect"},
	{http.MethodGet, admin.Prefix + "snapshot/"},
	{http.MethodGet, admin.Prefix + "bogus"},
}

func serve(c http.Handler, method, path, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)

	return rec
}

func TestAdminTokenRejectsUnauthenticated(t *testing.T) {
	c, resets := tokenControl(testToken)

	for _, authz := range []string{"", "Bearer wrong-token", "Bearer " + testToken + "x", "Basic " + testToken, testToken, "Bearer"} {
		for _, ep := range gatedEndpoints {
			rec := serve(c, ep.method, ep.path, authz)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with %q = %d, want 401", ep.method, ep.path, authz, rec.Code)
			}

			if strings.Contains(rec.Body.String(), "AKIA") {
				t.Errorf("%s %s with %q leaked the snapshot body", ep.method, ep.path, authz)
			}

			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("%s %s: WWW-Authenticate = %q, want a Bearer challenge", ep.method, ep.path, got)
			}
		}
	}

	if *resets != 0 {
		t.Fatalf("an unauthenticated reset ran %d times", *resets)
	}
}

func TestAdminTokenAcceptsValidToken(t *testing.T) {
	c, resets := tokenControl(testToken)

	for _, ep := range gatedEndpoints[:6] {
		if rec := serve(c, ep.method, ep.path, "Bearer "+testToken); rec.Code != http.StatusOK {
			t.Errorf("%s %s with the admin token = %d, want 200 (%s)", ep.method, ep.path, rec.Code, rec.Body.String())
		}
	}

	// The auth scheme is case-insensitive, as in RFC 7235.
	if rec := serve(c, http.MethodPost, admin.Prefix+"reset", "bearer "+testToken); rec.Code != http.StatusOK {
		t.Errorf("lower-case bearer scheme = %d, want 200", rec.Code)
	}

	if *resets != 2 {
		t.Fatalf("reset ran %d times, want 2", *resets)
	}
}

func TestAdminTokenHealthStaysOpen(t *testing.T) {
	c, _ := tokenControl(testToken)

	if rec := serve(c, http.MethodGet, admin.Prefix+"health", ""); rec.Code != http.StatusOK {
		t.Fatalf("health without a token = %d, want 200", rec.Code)
	}
}

func TestAdminTokenLeavesCloudAPIAlone(t *testing.T) {
	c, _ := tokenControl(testToken)

	// Requests outside /_cloudemu/ are the emulated cloud APIs, which carry their
	// own SigV4/Bearer credentials; the admin gate must not touch them.
	rec := serve(c, http.MethodGet, "/some/aws/request", "AWS4-HMAC-SHA256 Credential=AKIA/...")
	if rec.Code != http.StatusOK || rec.Body.String() != "backend" {
		t.Fatalf("cloud API request = %d %q, want 200 backend", rec.Code, rec.Body.String())
	}
}

func TestAdminTokenUnsetKeepsEndpointsOpen(t *testing.T) {
	c, _ := tokenControl("")

	for _, ep := range gatedEndpoints[:6] {
		if rec := serve(c, ep.method, ep.path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s %s with no token configured = %d, want 200", ep.method, ep.path, rec.Code)
		}
	}
}
