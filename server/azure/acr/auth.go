package acr

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// Real ACR's data-plane requires challenge-based bearer auth: an unauthenticated
// request gets a 401 with a WWW-Authenticate challenge, the client exchanges an
// AAD token for an ACR refresh token at POST /oauth2/exchange, then a refresh
// token for an ACR access token at POST /oauth2/token, and retries the original
// request with the access token attached.
//
// azcontainerregistry's Client always performs this dance BEFORE sending the
// real request body on the first call on a fresh Client: it clones the request
// with its body stripped, sends that as the "challenge probe", and only resends
// the original (with its body restored) if the probe comes back 401. Since the
// mock does not verify credentials, it only needs to complete the round trip —
// any bearer token is accepted — but it must actually perform it, or a
// body-bearing call (PATCH) silently loses its body: the probe would otherwise
// get the real 200 response and the SDK would never resend with the body.
//
// GET/DELETE requests have no body, so this round trip was invisible before
// changeableAttributes added the first ACR data-plane writes with a request
// body.
const (
	oauthExchangePath = "/oauth2/exchange"
	oauthTokenPath    = "/oauth2/token" //nolint:gosec // not a credential: a URL path, flagged only because it contains "token"

	// fakeAccessToken is the opaque bearer token minted by /oauth2/token. The
	// mock never inspects it — any bearer token is accepted — it only needs to
	// exist so the SDK's auth policy considers the challenge satisfied.
	fakeAccessToken = "cloudemu-fake-acr-access-token" //nolint:gosec // not a credential: a fixed opaque mock token, never validated

	// fakeRefreshTokenTTL is the "exp" claim minted into the fake refresh
	// token. authentication_policy.go parses it as a real JWT and errors if it
	// cannot find a future expiry, so it must be a well-formed (if fake) claim.
	fakeRefreshTokenTTL = 24 * time.Hour

	// maxOAuthFormBytes bounds the request body ParseForm reads for the two
	// token endpoints, generously larger than any real token-exchange form.
	maxOAuthFormBytes = 64 * 1024
)

// challenge writes a 401 with a WWW-Authenticate challenge for repo (or the
// catalog when repo is empty), matching the header shape
// authentication_policy.go's findServiceAndScope parses: comma-separated
// key="value" pairs directly after "Bearer ", no spaces.
func challenge(w http.ResponseWriter, repo string) {
	scope := "registry:catalog:*"
	if repo != "" {
		scope = fmt.Sprintf("repository:%s:*", repo)
	}

	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer realm="https://%s/oauth2/token",service=%q,scope=%q`,
		registryLoginServer, registryLoginServer, scope,
	))
	writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
}

// serveOAuthExchange serves POST /oauth2/exchange (AAD access/refresh token ->
// ACR refresh token). The mock does not verify the submitted AAD token; it
// mints an opaque, well-formed refresh token unconditionally.
func serveOAuthExchange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthFormBytes)

	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed oauth2/exchange form body")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"refresh_token": mintFakeRefreshToken()})
}

// serveOAuthToken serves POST /oauth2/token (ACR refresh token -> ACR access
// token). The mock does not verify the submitted refresh token.
func serveOAuthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthFormBytes)

	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed oauth2/token form body")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"access_token": fakeAccessToken})
}

// mintFakeRefreshToken builds an opaque but JWT-shaped (three dot-separated
// base64url segments) token carrying a far-future "exp" claim, satisfying
// authentication_policy.go's getJWTExpireTime without asserting anything about
// a real credential.
func mintFakeRefreshToken() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	exp := time.Now().Add(fakeRefreshTokenTTL).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))

	return header + "." + payload + ".cloudemu"
}
