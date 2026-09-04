package aws

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// tempCredentialPrefix marks STS-issued temporary access keys (ASIA). Their
// secret is minted by STS, which — when EnforceAuth is on — records it in the
// session store so the signature made with it can be SigV4-verified here exactly
// like a long-term AKIA key, and a forged ASIA credential (unknown/wrong secret)
// is rejected.
const tempCredentialPrefix = "ASIA"

// newAuthGate builds the SigV4 authentication pre-dispatch hook. It buffers and
// restores the request body (downstream Matches/ParseForm read it), resolves
// the caller's secret via the IAM access-key resolver (long-term AKIA keys) or
// the STS session store (temporary ASIA credentials), verifies the signature,
// and either attaches the resolved principal to the request context (proceed)
// or writes a 403 AWS error (stop). clock drives timestamp-expiry evaluation.
func newAuthGate(
	iamDriver iamdriver.IAM, accountID string, sessions *stssrv.SessionStore, clock config.Clock,
) func(http.ResponseWriter, *http.Request) (*http.Request, bool) {
	resolver, _ := iamDriver.(iamdriver.AccessKeyResolver)

	if clock == nil {
		clock = config.RealClock{}
	}

	return func(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		body := drainBody(r)
		restore := func() { r.Body = io.NopCloser(bytes.NewReader(body)) }

		akid := sigv4.AccessKeyID(r)
		if akid == "" {
			restore()
			writeAuthError(w, r, &sigv4.AuthError{
				Code:       "MissingAuthenticationToken",
				Message:    "Request is missing Authentication Token",
				HTTPStatus: http.StatusForbidden,
			})

			return r, false
		}

		// Temporary STS credentials are verified against the secret STS recorded
		// when it minted them (resolved from the session store), so a forged ASIA
		// credential and an expired session are both rejected.
		if strings.HasPrefix(akid, tempCredentialPrefix) {
			principal, aerr := verifyTempCredential(r, body, akid, accountID, sessions, clock)

			restore()

			if aerr != nil {
				writeAuthError(w, r, aerr)
				return r, false
			}

			return withPrincipal(r, principal), true
		}

		principal, aerr := sigv4.Verify(r, body, resolverLookup(r, resolver), clock)

		restore()

		if aerr != nil {
			writeAuthError(w, r, aerr)
			return r, false
		}

		if !authorize(w, r, principal, iamDriver, body, accountID) {
			return r, false
		}

		return withPrincipal(r, principal), true
	}
}

// verifyTempCredential authenticates an STS temporary (ASIA) credential. It
// resolves the secret STS recorded for the presented access key id, rejects an
// unknown key (InvalidClientTokenId) or an expired session (ExpiredToken), then
// SigV4-verifies the signature against that secret. When no session store is
// wired the credential is unverifiable, so it fails closed.
func verifyTempCredential(
	r *http.Request, body []byte, akid, accountID string, sessions *stssrv.SessionStore, clock config.Clock,
) (authctx.Principal, *sigv4.AuthError) {
	invalid := &sigv4.AuthError{
		Code:       "InvalidClientTokenId",
		Message:    "The security token included in the request is invalid.",
		HTTPStatus: http.StatusForbidden,
	}

	if sessions == nil {
		return authctx.Principal{}, invalid
	}

	sess, ok := sessions.Lookup(akid)
	if !ok {
		return authctx.Principal{}, invalid
	}

	if clock.Now().UTC().After(sess.Expiration) {
		return authctx.Principal{}, &sigv4.AuthError{
			Code:       "ExpiredToken",
			Message:    "The security token included in the request is expired.",
			HTTPStatus: http.StatusForbidden,
		}
	}

	lookup := func(id string) (string, authctx.Principal, bool) {
		return sess.SecretAccessKey, authctx.Principal{AccessKeyID: id, AccountID: accountID}, true
	}

	return sigv4.Verify(r, body, lookup, clock)
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
