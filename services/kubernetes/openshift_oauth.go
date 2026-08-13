package kubernetes

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// OpenShift OAuth server — the surface `oc login` drives.
//
// `oc login <server> -u <user> -p <pass>` runs a challenging-client flow:
//  1. GET <server>/.well-known/oauth-authorization-server for the endpoints.
//  2. GET the authorization_endpoint with response_type=token; when unauthenticated
//     the server challenges with WWW-Authenticate: Basic, oc retries with the
//     credentials, and the server 302-redirects with the token in the URL fragment.
//  3. oc extracts #access_token and writes it into the kubeconfig, then calls
//     GET users/~ (whoami) to confirm the identity.
//
// A real cluster serves this from a dedicated oauth-openshift.apps.* host and
// authenticates against a real identity provider. The emulator serves it inline
// under the cluster's own URL and accepts ANY credentials (it is an
// unauthenticated test backend) — the point is that the wire flow completes so
// `oc login` works, not that access is actually gated.
const (
	oauthWellKnownPath = "/.well-known/oauth-authorization-server"
	oauthAuthorizePath = "/oauth/authorize"
	oauthTokenPath     = "/oauth/token" //nolint:gosec // endpoint path, not a credential.
	oauthImplicitPath  = "/oauth/token/implicit"

	// oauthDefaultUser is the identity returned when a caller presents no
	// credentials (matches the well-known bootstrap admin name).
	oauthDefaultUser = "kubeadmin"
)

// isOpenShiftOAuthPath reports whether an already-prefix-stripped request path
// targets the OpenShift OAuth server.
func isOpenShiftOAuthPath(path string) bool {
	return path == oauthWellKnownPath || strings.HasPrefix(path, "/oauth/")
}

// isWhoamiPath reports whether path is the `oc whoami` self-user lookup.
func isWhoamiPath(path string) bool {
	return path == "/apis/user.openshift.io/v1/users/~"
}

// serveOAuth dispatches the OAuth discovery / authorize / token endpoints.
// absBase is the cluster's absolute URL (scheme://host/k8s/<uid>) — the OAuth
// metadata must advertise absolute endpoints, so the caller (APIServer.ServeHTTP)
// computes it from the live request before the /k8s/<uid> prefix is stripped.
func (s *ClusterState) serveOAuth(w http.ResponseWriter, r *http.Request, absBase string) {
	switch r.URL.Path {
	case oauthWellKnownPath:
		serveOAuthMetadata(w, absBase)
	case oauthAuthorizePath:
		s.serveOAuthAuthorize(w, r)
	case oauthTokenPath:
		s.serveOAuthToken(w, r)
	default:
		writeNotFound(w, "openshift oauth: unrecognized path "+r.URL.Path)
	}
}

// serveOAuthMetadata answers RFC 8414 authorization-server metadata, matching
// the shape captured from a live OCP cluster.
func serveOAuthMetadata(w http.ResponseWriter, absBase string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                 absBase,
		"authorization_endpoint": absBase + oauthAuthorizePath,
		"token_endpoint":         absBase + oauthTokenPath,
		"scopes_supported": []string{
			"user:check-access", "user:full", "user:info", "user:list-projects", "user:list-scoped-projects",
		},
		"response_types_supported":         []string{"code", "token"},
		"grant_types_supported":            []string{"authorization_code", "implicit"},
		"code_challenge_methods_supported": []string{"plain", "S256"},
	})
}

// serveOAuthAuthorize implements the challenging-client authorize endpoint. With
// no credentials it issues a Basic challenge; with credentials it mints a token
// and 302-redirects with the token in the fragment (response_type=token) or a
// code in the query (response_type=code).
func (s *ClusterState) serveOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	user, ok := basicAuthUser(r)
	if !ok {
		// Challenge — oc retries the request with Authorization: Basic.
		w.Header().Set("WWW-Authenticate", `Basic realm="openshift"`)
		writeStatus(w, http.StatusUnauthorized, metav1.StatusReasonUnauthorized,
			"openshift oauth: authentication required")

		return
	}

	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")

	token := s.mintOAuthToken(user)

	if q.Get("response_type") == "code" {
		redirectWithCode(w, r, redirectURI, q.Get("state"), token)

		return
	}

	// Default (response_type=token) — the implicit flow oc login uses.
	if redirectURI == "" {
		redirectURI = oauthImplicitPath
	}

	frag := url.Values{
		"access_token": {token},
		"expires_in":   {"86400"},
		"scope":        {"user:full"},
		"token_type":   {"Bearer"},
	}
	if st := q.Get("state"); st != "" {
		frag.Set("state", st)
	}

	http.Redirect(w, r, redirectURI+"#"+frag.Encode(), http.StatusFound)
}

// redirectWithCode 302-redirects an authorization-code response. The emulator
// reuses the minted token as the code (a real server exchanges the code at the
// token endpoint; here the token endpoint just re-mints, so either resolves).
func redirectWithCode(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	if redirectURI == "" {
		writeBadRequest(w, "openshift oauth: redirect_uri required for code flow")

		return
	}

	q := url.Values{"code": {code}}
	if state != "" {
		q.Set("state", state)
	}

	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}

	http.Redirect(w, r, redirectURI+sep+q.Encode(), http.StatusFound)
}

// serveOAuthToken implements the token endpoint (authorization_code grant that
// oc's PKCE flow uses). The request's Basic auth is the CLIENT
// (openshift-challenging-client), not the user — the user identity rides on the
// `code`, which redirectWithCode set to a user-bound token minted at authorize.
// So the code is looked up to recover the user and returned as the access token;
// only when there is no resolvable code does it fall back to minting a fresh one.
func (s *ClusterState) serveOAuthToken(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code") // ParseForm reads the urlencoded POST body.

	token := code
	if token == "" || s.userForToken(token) == "" {
		token = s.mintOAuthToken(oauthDefaultUser)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   86400,
		"scope":        "user:full",
	})
}

// userForToken returns the user a token was minted for, or "" if unknown.
func (s *ClusterState) userForToken(token string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.oauthTokens[token]
}

// serveWhoami answers GET users/~ — the self-user lookup `oc whoami` and
// `oc login` use to confirm the identity. It resolves the caller from the
// bearer token minted at login, defaulting to the bootstrap admin.
func (s *ClusterState) serveWhoami(w http.ResponseWriter, r *http.Request) {
	user := s.userForRequest(r)

	s.mu.RLock()
	st := s.reg.getStore(apiGroupOSUser, "v1", "users")

	var stored *unstructured.Unstructured
	if st != nil {
		stored = st.items[objKey("", user)]
	}
	s.mu.RUnlock()

	if stored != nil {
		writeJSON(w, http.StatusOK, stored.DeepCopy())

		return
	}

	writeJSON(w, http.StatusOK, newUserObject(user))
}

// userForRequest resolves the username for a request from its bearer token, or
// the default admin when the token is unknown/absent (the emulator is
// unauthenticated, so an unrecognized token still resolves to a usable identity).
func (s *ClusterState) userForRequest(r *http.Request) string {
	token := bearerToken(r)
	if token == "" {
		return oauthDefaultUser
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if u, ok := s.oauthTokens[token]; ok {
		return u
	}

	return oauthDefaultUser
}

// mintOAuthToken issues a fresh OpenShift-style bearer token for user, records
// the token->user mapping for whoami, and upserts the User object so a
// subsequent `oc get user` sees it.
func (s *ClusterState) mintOAuthToken(user string) string {
	token := "sha256~" + newUID() + newUID()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.oauthTokens[token] = user

	if st := s.reg.getStore(apiGroupOSUser, "v1", "users"); st != nil {
		key := objKey("", user)
		if _, exists := st.items[key]; !exists {
			st.items[key] = newUserObject(user)
		}
	}

	return token
}

// newUserObject builds a user.openshift.io/v1 User with the default groups a
// real cluster reports for an authenticated OAuth identity.
func newUserObject(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupOSUser + "/v1",
		"kind":       "User",
		"metadata": map[string]any{
			"name":              name,
			"creationTimestamp": nil,
		},
		"fullName":   name,
		"identities": []any{"cloudemu:" + name},
		"groups":     []any{"system:authenticated", "system:authenticated:oauth"},
	}}
	u.SetUID(types.UID(newUID()))
	u.SetResourceVersion("1")

	return u
}

// basicAuthUser extracts the username from a Basic Authorization header. The
// password is ignored — the emulator authenticates no one. Returns ok=false
// when no Basic credentials are present (so the caller can issue a challenge).
func basicAuthUser(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Basic ") {
		return "", false
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic "))
	if err != nil {
		return "", false
	}

	user, _, found := strings.Cut(string(raw), ":")
	if !found || user == "" {
		return "", false
	}

	return user, true
}

// bearerToken extracts a Bearer token from the Authorization header, or "".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}

	return strings.TrimPrefix(h, "Bearer ")
}
