package aws

import (
	"bytes"
	"crypto/subtle"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// tempCredentialPrefix marks STS-issued temporary access keys (ASIA). Their
// secret is minted by STS, which, when EnforceAuth is on, records it in the
// session store so the signature made with it can be SigV4-verified here like
// a long-term AKIA key, and a forged ASIA credential (unknown/wrong secret)
// is rejected.
const tempCredentialPrefix = "ASIA"

// securityTokenParam carries an STS session token, as a header or, on a
// presigned URL, a query parameter.
const securityTokenParam = "X-Amz-Security-Token" //nolint:gosec // a header name, not a credential

// gateConfig is what the auth gate needs from the server it guards.
type gateConfig struct {
	iam      iamdriver.IAM
	scope    awsauthz.Scope
	sessions *stssrv.SessionStore
	clock    config.Clock
	// match is the dispatcher's handler lookup. The public exemption and the
	// authorization plan are bound to the handler it returns.
	match func(*http.Request) server.Handler
	// jsonRPC holds the handlers whose operation is named by X-Amz-Target
	// (see jsonRPCServiceByTarget). The header is never read for any other
	// handler, so a forged one cannot steer the authorized action.
	jsonRPC map[server.Handler]bool
	// authnOnly holds the handlers IAM does not govern (the Kubernetes data
	// plane, which has its own RBAC). Signed requests to them are only
	// authenticated.
	authnOnly map[server.Handler]bool
}

// newAuthGate builds the SigV4 pre-dispatch hook. It buffers and restores the
// request body (downstream Matches/ParseForm read it), finds the handler
// dispatch will pick, lets that handler's public (noAuth) operations through,
// resolves the caller's secret via the IAM access-key resolver (long-term
// AKIA keys) or the STS session store (temporary ASIA credentials), verifies
// the signature, and then authorizes the request against the caller's IAM
// policies. It either attaches the principal to the request context
// (proceed) or writes a 403 AWS error (stop).
func newAuthGate(g *gateConfig) func(http.ResponseWriter, *http.Request) (*http.Request, bool) {
	resolver, _ := g.iam.(iamdriver.AccessKeyResolver)

	if g.clock == nil {
		g.clock = config.RealClock{}
	}

	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		body := drainBody(r)
		restore := func() { r.Body = io.NopCloser(bytes.NewReader(body)) }

		restore()

		probe, h, probed := probeRoute(r, body, g.match)

		if probed && servedPublicly(probe, h) {
			return r, true
		}

		akid := sigv4.AccessKeyID(r)
		if akid == "" {
			writeAuthError(w, r, &sigv4.AuthError{
				Code:       "MissingAuthenticationToken",
				Message:    "Request is missing Authentication Token",
				HTTPStatus: http.StatusForbidden,
			})

			return r, false
		}

		// Temporary STS credentials are verified against the secret STS recorded
		// when it minted them (resolved from the session store), so a forged ASIA
		// credential and an expired session are both rejected. The session is
		// then authorized as its owner: a role session strictly against the
		// role's policies, a GetSessionToken session as the user that minted it.
		var (
			principal   authctx.Principal
			kind        stssrv.SessionKind
			aerr        *sigv4.AuthError
		)

		if strings.HasPrefix(akid, tempCredentialPrefix) {
			principal, kind, aerr = verifyTempCredential(r, body, akid, g.scope.AccountID, g.sessions, g.clock)
		} else {
			principal, aerr = sigv4.Verify(r, body, resolverLookup(r, resolver), g.clock)
		}

		restore()

		if aerr != nil {
			writeAuthError(w, r, aerr)
			return r, false
		}

		plan := g.resolvePlan(probe, h, probed, body)

		return g.authorize(w, r, h, plan, &principal, kind)
	}
}

// verifyTempCredential authenticates an STS temporary (ASIA) credential. It
// resolves the secret STS recorded for the presented access key id, rejects an
// unknown key or one sent without its session token (InvalidClientTokenId) or an
// expired session (ExpiredToken), then
// SigV4-verifies the signature against that secret. When no session store is
// wired the credential is unverifiable, so it fails closed. The principal is
// the session's owner (see stssrv.SessionOwner), and kind is the
// kind of session.
func verifyTempCredential(
	r *http.Request, body []byte, akid, accountID string, sessions *stssrv.SessionStore, clock config.Clock,
) (principal authctx.Principal, kind stssrv.SessionKind, aerr *sigv4.AuthError) {
	invalid := &sigv4.AuthError{
		Code:       "InvalidClientTokenId",
		Message:    "The security token included in the request is invalid.",
		HTTPStatus: http.StatusForbidden,
	}

	if sessions == nil {
		return authctx.Principal{}, stssrv.KindNone, invalid
	}

	sess, ok := sessions.Lookup(akid)
	if !ok || !sessionTokenMatches(r, sess.SessionToken) {
		return authctx.Principal{}, stssrv.KindNone, invalid
	}

	if clock.Now().UTC().After(sess.Expiration) {
		return authctx.Principal{}, stssrv.KindNone, &sigv4.AuthError{
			Code:       "ExpiredToken",
			Message:    "The security token included in the request is expired.",
			HTTPStatus: http.StatusForbidden,
		}
	}

	lookup := func(id string) (string, authctx.Principal, bool) {
		return sess.SecretAccessKey, authctx.Principal{
			AccessKeyID: id,
			AccountID:   accountID,
			UserName:    sess.Owner.PolicyEntity,
			ARN:         sess.Owner.ARN,
			UserID:      sess.Owner.UserID,
		}, true
	}

	principal, aerr = sigv4.Verify(r, body, lookup, clock)

	return principal, sess.Owner.Kind, aerr
}

// sessionTokenMatches reports whether the request carries the session token
// STS issued with the credential, in the X-Amz-Security-Token header or, for a
// presigned URL, the query string. Real STS rejects a temporary key id presented
// without its token.
func sessionTokenMatches(r *http.Request, want string) bool {
	got := r.Header.Get(securityTokenParam)
	if got == "" {
		got = r.URL.Query().Get(securityTokenParam)
	}

	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// resolverLookup adapts the IAM access-key resolver to sigv4.LookupFunc,
// threading the request context. It reports ok=false when no resolver is wired
// or the key is unknown, so an unresolved key becomes InvalidClientTokenId.
func resolverLookup(r *http.Request, resolver iamdriver.AccessKeyResolver) sigv4.LookupFunc {
	return func(id string) (string, authctx.Principal, bool) {
		if resolver == nil {
			return "", authctx.Principal{}, false
		}

		info, ok := resolver.AccessKeyByID(r.Context(), id)
		if !ok {
			return "", authctx.Principal{}, false
		}

		return info.SecretAccessKey, authctx.Principal{
			AccessKeyID: info.AccessKeyID,
			UserName:    info.UserName,
			ARN:         info.UserARN,
			AccountID:   info.AccountID,
			UserID:      info.UserID,
		}, true
	}
}

// drainBody reads and closes the request body, returning its bytes. A nil body
// yields an empty slice.
func drainBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}

	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	return body
}

func withPrincipal(r *http.Request, p authctx.Principal) *http.Request {
	return r.WithContext(authctx.WithPrincipal(r.Context(), p))
}

// writeAuthError renders a 403 in the wire format the request expects: JSON for
// JSON-RPC / REST-JSON services (X-Amz-Target or a JSON content type), XML for
// the query and S3 protocols.
func writeAuthError(w http.ResponseWriter, r *http.Request, aerr *sigv4.AuthError) {
	if isJSONProtocol(r) {
		wire.WriteJSONError(w, aerr.HTTPStatus, aerr.Code, aerr.Message)
		return
	}

	awsquery.WriteXMLError(w, aerr.HTTPStatus, aerr.Code, aerr.Message)
}

func isJSONProtocol(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return true
	}

	return strings.Contains(r.Header.Get("Content-Type"), "json")
}
