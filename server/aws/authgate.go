package aws

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	"github.com/stackshy/cloudemu/v2/server/wire/sigv4"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// tempCredentialPrefix marks STS-issued temporary access keys. Their secret is
// synthetic (derived by STS, not stored as an IAM access key), so their SigV4
// signature cannot be verified here; such requests are treated as authenticated
// pass-throughs in this revision, and verifying temporary-credential signatures
// is a follow-up. Note the limitation this leaves: unsigned or malformed
// requests are still rejected, but ANY credential whose scope carries an
// ASIA-prefixed key id is trusted without a signature check — so this is not yet
// a hard boundary against a forged ASIA credential. That is acceptable for a
// local dev emulator (rejecting all ASIA would break legitimate STS/IRSA/
// instance-profile flows) and is closed when temp-credential verification lands.
const tempCredentialPrefix = "ASIA"

// newAuthGate builds the SigV4 authentication pre-dispatch hook. It buffers and
// restores the request body (downstream Matches/ParseForm read it), resolves
// the caller's secret via the IAM access-key resolver, verifies the signature,
// and either attaches the resolved principal to the request context (proceed)
// or writes a 403 AWS error (stop).
func newAuthGate(iamDriver iamdriver.IAM, accountID string) func(http.ResponseWriter, *http.Request) (*http.Request, bool) {
	resolver, _ := iamDriver.(iamdriver.AccessKeyResolver)

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

		// Temporary STS credentials cannot be SigV4-verified (synthetic secret);
		// pass them through as authenticated for now (documented follow-up).
		if strings.HasPrefix(akid, tempCredentialPrefix) {
			restore()

			return withPrincipal(r, authctx.Principal{AccessKeyID: akid, AccountID: accountID}), true
		}

		principal, aerr := sigv4.Verify(r, body, resolverLookup(r, resolver), config.RealClock{})

		restore()

		if aerr != nil {
			writeAuthError(w, r, aerr)
			return r, false
		}

		return withPrincipal(r, principal), true
	}
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
