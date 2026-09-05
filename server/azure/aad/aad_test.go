package aad

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

const testTenant = "11111111-1111-1111-1111-111111111111"

// tlsState is a non-nil ConnectionState so a test request reports an https
// scheme to baseURL (its presence, not its contents, is what matters).
var tlsState = tls.ConnectionState{}

func TestMetadataMatches(t *testing.T) {
	h := NewMetadata()

	if !h.Matches(httptest.NewRequest(http.MethodGet, "/metadata/endpoints", nil)) {
		t.Fatal("expected GET /metadata/endpoints to match")
	}

	// go-azure-sdk sometimes emits a doubled leading slash; it must still match.
	if !h.Matches(httptest.NewRequest(http.MethodGet, "//metadata/endpoints", nil)) {
		t.Fatal("expected GET //metadata/endpoints (doubled slash) to match")
	}

	if h.Matches(httptest.NewRequest(http.MethodPost, "/metadata/endpoints", nil)) {
		t.Fatal("POST /metadata/endpoints must not match")
	}

	if h.Matches(httptest.NewRequest(http.MethodGet, "/subscriptions/x", nil)) {
		t.Fatal("an ARM path must not match the metadata handler")
	}
}

func TestMetadataDocumentSelfReferential(t *testing.T) {
	h := NewMetadata()

	req := httptest.NewRequest(http.MethodGet, "/metadata/endpoints", nil)
	req.Host = "127.0.0.1:16000"
	req.TLS = &tlsState

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != contentTypeJSON {
		t.Fatalf("content-type = %q", ct)
	}

	var doc metadataDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	const wantBase = "https://127.0.0.1:16000"

	// The three fields go-azure-sdk's FromEndpoint validates as required.
	if doc.Name == "" {
		t.Error("name must be non-empty")
	}

	if doc.ResourceManager != wantBase+"/" {
		t.Errorf("resourceManager = %q, want %q", doc.ResourceManager, wantBase+"/")
	}

	if doc.MicrosoftGraphResourceID == "" {
		t.Error("microsoftGraphResourceId must be non-empty")
	}

	if doc.Authentication.LoginEndpoint != wantBase {
		t.Errorf("loginEndpoint = %q, want %q", doc.Authentication.LoginEndpoint, wantBase)
	}

	// Must be "common" so go-azure-sdk does not classify the environment as Azure
	// Stack (which azurerm then refuses to configure).
	if doc.Authentication.Tenant != "common" {
		t.Errorf("tenant = %q, want %q", doc.Authentication.Tenant, "common")
	}

	if doc.Authentication.IdentityProvider != "AAD" {
		t.Errorf("identityProvider = %q, want AAD", doc.Authentication.IdentityProvider)
	}

	if len(doc.Authentication.Audiences) == 0 {
		t.Error("audiences must be non-empty")
	}
}

func TestMetadataBaseURLSchemeFromTLS(t *testing.T) {
	h := NewMetadata()

	// No TLS on the request → http scheme.
	req := httptest.NewRequest(http.MethodGet, "/metadata/endpoints", nil)
	req.Host = "example:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var doc metadataDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if doc.ResourceManager != "http://example:1234/" {
		t.Errorf("resourceManager = %q, want http scheme", doc.ResourceManager)
	}
}

func TestTokenMatches(t *testing.T) {
	h := NewToken(testTenant)

	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/" + testTenant + "/oauth2/v2.0/token", true},
		{http.MethodPost, "/" + testTenant + "/oauth2/token", true},
		{http.MethodGet, "/" + testTenant + "/oauth2/v2.0/token", false},
		{http.MethodPost, "/subscriptions/x/resourceGroups/y", false},
		{http.MethodPost, "/" + testTenant + "/oauth2/v2.0/authorize", false},
	}

	for _, c := range cases {
		got := h.Matches(httptest.NewRequest(c.method, c.path, nil))
		if got != c.want {
			t.Errorf("Matches(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestTokenResponseShapeAndClaims(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h := NewTokenWithClock(testTenant, config.NewFakeClock(now))

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"33333333-3333-3333-3333-333333333333"},
		"client_secret": {"anything"},
		"scope":         {"https://management.azure.com/.default"},
	}
	req := httptest.NewRequest(http.MethodPost, "/"+testTenant+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "127.0.0.1:16000"
	req.TLS = &tlsState

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}

	if resp.ExpiresIn != 3600 || resp.ExtExpiresIn != 3600 {
		t.Errorf("expires_in/ext = %d/%d, want 3600/3600", resp.ExpiresIn, resp.ExtExpiresIn)
	}

	// The access token must be a decodable 3-part JWT carrying oid/tid/appid so a
	// client resolves the principal without a Graph lookup.
	parts := strings.Split(resp.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("access_token has %d parts, want 3", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	if c.ObjectID != bootstrapObjectID {
		t.Errorf("oid = %q, want %q", c.ObjectID, bootstrapObjectID)
	}

	if c.TenantID != testTenant {
		t.Errorf("tid = %q, want %q", c.TenantID, testTenant)
	}

	if c.AppID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("appid = %q, want the request client_id", c.AppID)
	}

	if c.Expires != now.Add(time.Hour).Unix() {
		t.Errorf("exp = %d, want %d", c.Expires, now.Add(time.Hour).Unix())
	}
}

func TestTokenDefaultAppIDWhenNoClientID(t *testing.T) {
	h := NewToken(testTenant)

	req := httptest.NewRequest(http.MethodPost, "/"+testTenant+"/oauth2/v2.0/token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	parts := strings.Split(resp.AccessToken, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])

	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	if c.AppID != defaultAppID {
		t.Errorf("appid = %q, want default %q", c.AppID, defaultAppID)
	}
}

func TestTenantForRequest(t *testing.T) {
	h := NewToken("fallback-tenant")

	cases := map[string]string{
		"/" + testTenant + "/oauth2/v2.0/token": testTenant,
		"/" + testTenant + "/oauth2/token":      testTenant,
		"/oauth2/v2.0/token":                    "fallback-tenant",
	}

	for path, want := range cases {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if got := h.tenantForRequest(req); got != want {
			t.Errorf("tenantForRequest(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestIsBootstrapPath(t *testing.T) {
	yes := []string{"/metadata/endpoints", "/" + testTenant + "/oauth2/v2.0/token", "/tenant/oauth2/token"}
	no := []string{"/subscriptions/x", "/metadata/other", "/foo"}

	for _, p := range yes {
		if !IsBootstrapPath(p) {
			t.Errorf("IsBootstrapPath(%q) = false, want true", p)
		}
	}

	for _, p := range no {
		if IsBootstrapPath(p) {
			t.Errorf("IsBootstrapPath(%q) = true, want false", p)
		}
	}
}
