package aad

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// Token endpoint path suffixes. azurerm/go-azure-sdk uses the v2 (MSAL) form;
// the v1 (ADAL) form is accepted too so other tools/SDKs still resolve.
const (
	tokenSuffixV2 = "/oauth2/v2.0/token" //nolint:gosec // G101 false positive: an OAuth2 URL path suffix, not a credential
	tokenSuffixV1 = "/oauth2/token"      //nolint:gosec // G101 false positive: an OAuth2 URL path suffix, not a credential
)

// tokenLifetime is how long the issued bearer is valid. One hour matches a real
// AAD access token and is comfortably longer than any single terraform run.
const tokenLifetime = time.Hour

// defaultAppID is the appid claim used when the request carries no client_id.
const defaultAppID = "00000000-0000-0000-0000-000000000001"

// maxFormBytes bounds the token-request body read, so a client cannot exhaust
// memory through the form parser. A client-credentials form is a few hundred
// bytes; 64 KiB is generous headroom.
const maxFormBytes = 64 << 10

// bootstrapObjectID is the service-principal object id (oid claim) reported to
// the client. A stable, well-formed GUID lets azurerm resolve the authenticated
// object id straight from the token, so it never falls back to a Microsoft Graph
// lookup.
const bootstrapObjectID = "22222222-2222-2222-2222-222222222222"

// TokenHandler serves the OAuth2 client-credentials token endpoint. It returns
// a well-formed, fake-signed JWT for any request — CloudEmu accepts any
// credentials, so it neither reads the client secret nor verifies anything; it
// only has to hand back a token the client can decode and present.
type TokenHandler struct {
	tenantID string
	clock    config.Clock
}

// NewToken returns a token handler. tenantID is the fallback tenant used in the
// issued claims when the request path carries none.
func NewToken(tenantID string) *TokenHandler {
	return &TokenHandler{tenantID: tenantID, clock: config.RealClock{}}
}

// NewTokenWithClock is NewToken with an injectable clock for deterministic tests.
func NewTokenWithClock(tenantID string, clock config.Clock) *TokenHandler {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &TokenHandler{tenantID: tenantID, clock: clock}
}

// Matches claims POST requests to an OAuth2 token path.
func (*TokenHandler) Matches(r *http.Request) bool {
	return r.Method == http.MethodPost && isTokenPath(r.URL.Path)
}

// ServeHTTP issues the bearer-token response.
func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant := h.tenantForRequest(r)

	appID := defaultAppID

	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	}

	if err := r.ParseForm(); err == nil {
		if v := r.PostFormValue("client_id"); v != "" {
			appID = v
		}
	}

	now := h.clock.Now()
	audience := baseURL(r) + "/"
	access := signedJWT(&claims{
		Audience:  audience,
		Issuer:    "https://sts.windows.net/" + tenant + "/",
		IssuedAt:  now.Unix(),
		NotBefore: now.Unix(),
		Expires:   now.Add(tokenLifetime).Unix(),
		ObjectID:  bootstrapObjectID,
		Subject:   bootstrapObjectID,
		TenantID:  tenant,
		AppID:     appID,
		AZP:       appID,
		IDType:    "app",
		Version:   "1.0",
	})

	writeJSON(w, http.StatusOK, tokenResponse{
		TokenType:    "Bearer",
		ExpiresIn:    int(tokenLifetime.Seconds()),
		ExtExpiresIn: int(tokenLifetime.Seconds()),
		AccessToken:  access,
	})
}

// tenantForRequest returns the tenant GUID from the request path
// (/{tenant}/oauth2/...), falling back to the handler's configured tenant.
func (h *TokenHandler) tenantForRequest(r *http.Request) string {
	prefix := strings.TrimSuffix(strings.TrimSuffix(r.URL.Path, tokenSuffixV2), tokenSuffixV1)
	prefix = strings.Trim(prefix, "/")

	// The tenant is the last remaining path segment before the oauth2 suffix.
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		prefix = prefix[i+1:]
	}

	if prefix == "" {
		return h.tenantID
	}

	return prefix
}

// isTokenPath reports whether p is an OAuth2 token endpoint path.
func isTokenPath(p string) bool {
	return strings.HasSuffix(p, tokenSuffixV2) || strings.HasSuffix(p, tokenSuffixV1)
}

// tokenResponse is the OAuth2 token endpoint's JSON body.
type tokenResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	ExtExpiresIn int    `json:"ext_expires_in"`
	AccessToken  string `json:"access_token"`
}

// claims is the subset of Azure AD JWT claims CloudEmu populates so a client can
// resolve the authenticated principal (oid/tid/appid) without a Graph lookup.
type claims struct {
	Audience  string `json:"aud"`
	Issuer    string `json:"iss"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Expires   int64  `json:"exp"`
	ObjectID  string `json:"oid"`
	Subject   string `json:"sub"`
	TenantID  string `json:"tid"`
	AppID     string `json:"appid"`
	AZP       string `json:"azp"`
	IDType    string `json:"idtyp"`
	Version   string `json:"ver"`
}

// signedJWT builds a three-segment JWT (header.payload.signature). The signature
// is a fixed placeholder: the token is never cryptographically verified —
// clients only base64-decode the payload to read the claims, and CloudEmu's ARM
// layer accepts any bearer.
func signedJWT(c *claims) string {
	header := segment(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "cloudemu"})
	payload := segment(c)
	signature := base64.RawURLEncoding.EncodeToString([]byte("cloudemu"))

	return header + "." + payload + "." + signature
}

// segment JSON-marshals v and base64url-encodes it without padding, the JWT
// segment encoding.
func segment(v any) string {
	b, _ := json.Marshal(v)

	return base64.RawURLEncoding.EncodeToString(b)
}
