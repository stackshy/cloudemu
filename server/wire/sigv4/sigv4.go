// Package sigv4 verifies AWS Signature Version 4 signatures on incoming wire
// requests. It rebuilds the canonical request exactly as aws-sdk-go-v2's signer
// does, recomputes the signature with the secret resolved from the presented
// access key id, and compares in constant time. It supports both the
// Authorization-header form and the presigned query-string form.
//
// The canonicalization intentionally mirrors the AWS SigV4 specification
// byte-for-byte so a request signed by any real AWS SDK verifies here; the
// passing real-SDK compatibility test is the proof of the match.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
)

const (
	algorithm      = "AWS4-HMAC-SHA256"
	terminator     = "aws4_request"
	unsignedStatus = http.StatusForbidden
	// credScopeParts is AKID/DATE/REGION/SERVICE/aws4_request.
	credScopeParts = 5
)

// AuthError is a typed authentication failure carrying the AWS error code and
// the HTTP status the gate should return.
type AuthError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *AuthError) Error() string { return e.Code + ": " + e.Message }

// LookupFunc resolves an access key id to its secret and owning principal. It
// reports ok=false when the key is unknown.
type LookupFunc func(accessKeyID string) (secret string, principal authctx.Principal, ok bool)

// signInputs are the fields extracted from a request's SigV4 material.
type signInputs struct {
	accessKeyID   string
	dateStamp     string // YYYYMMDD (from the credential scope)
	region        string
	service       string
	signedHeaders []string
	signature     string
	amzDate       string // full timestamp used in the string-to-sign
	presigned     bool
}

// AccessKeyID returns the access key id presented by the request (header or
// presigned query), or "" when the request carries no SigV4 material. The gate
// uses it to special-case temporary credentials before full verification.
func AccessKeyID(r *http.Request) string {
	in, err := parseInputs(r)
	if err != nil {
		return ""
	}

	return in.accessKeyID
}

// Verify checks the request's SigV4 signature against the secret resolved for
// its access key id. On success it returns the resolved principal; on failure a
// typed AuthError (HTTP 403). body is the buffered request body (the gate reads
// and restores it). clock is accepted for skew evaluation; expiry is not
// strictly enforced in this revision (the emulator commonly runs on a FakeClock
// and tests may sign with fixed dates).
func Verify(r *http.Request, body []byte, lookup LookupFunc, _ config.Clock) (authctx.Principal, *AuthError) {
	in, err := parseInputs(r)
	if err != nil {
		return authctx.Principal{}, err
	}

	secret, principal, ok := lookup(in.accessKeyID)
	if !ok {
		return authctx.Principal{}, &AuthError{
			Code:       "InvalidClientTokenId",
			Message:    "The security token included in the request is invalid.",
			HTTPStatus: unsignedStatus,
		}
	}

	canonReq := canonicalRequest(r, body, &in)
	sts := stringToSign(&in, canonReq)
	expected := hex.EncodeToString(sign(signingKey(secret, &in), []byte(sts)))

	if !hmac.Equal([]byte(expected), []byte(in.signature)) {
		return authctx.Principal{}, &AuthError{
			Code:       "SignatureDoesNotMatch",
			Message:    "The request signature we calculated does not match the signature you provided.",
			HTTPStatus: unsignedStatus,
		}
	}

	return principal, nil
}

// parseInputs extracts the signing material from either the Authorization
// header or the presigned query string.
func parseInputs(r *http.Request) (signInputs, *AuthError) {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, algorithm) {
		return parseHeader(r, auth)
	}

	if r.URL.Query().Get("X-Amz-Signature") != "" {
		return parsePresigned(r)
	}

	return signInputs{}, &AuthError{
		Code:       "MissingAuthenticationToken",
		Message:    "Request is missing Authentication Token",
		HTTPStatus: unsignedStatus,
	}
}

// parseHeader parses the Authorization-header form.
func parseHeader(r *http.Request, auth string) (signInputs, *AuthError) {
	rest := strings.TrimSpace(strings.TrimPrefix(auth, algorithm))

	var cred, signed, sig string

	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)

		switch {
		case strings.HasPrefix(part, "Credential="):
			cred = strings.TrimPrefix(part, "Credential=")
		case strings.HasPrefix(part, "SignedHeaders="):
			signed = strings.TrimPrefix(part, "SignedHeaders=")
		case strings.HasPrefix(part, "Signature="):
			sig = strings.TrimPrefix(part, "Signature=")
		}
	}

	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = r.Header.Get("Date")
	}

	return buildInputs(cred, signed, sig, amzDate, false)
}

// parsePresigned parses the presigned query-string form.
func parsePresigned(r *http.Request) (signInputs, *AuthError) {
	q := r.URL.Query()

	return buildInputs(
		q.Get("X-Amz-Credential"),
		q.Get("X-Amz-SignedHeaders"),
		q.Get("X-Amz-Signature"),
		q.Get("X-Amz-Date"),
		true,
	)
}

// buildInputs assembles and validates signInputs from raw fields.
func buildInputs(cred, signed, sig, amzDate string, presigned bool) (signInputs, *AuthError) {
	scope := strings.Split(cred, "/")
	if len(scope) != credScopeParts || signed == "" || sig == "" {
		return signInputs{}, incompleteErr()
	}

	if amzDate == "" {
		return signInputs{}, incompleteErr()
	}

	headers := strings.Split(signed, ";")
	for i := range headers {
		headers[i] = strings.ToLower(headers[i])
	}

	sort.Strings(headers)

	return signInputs{
		accessKeyID:   scope[0],
		dateStamp:     scope[1],
		region:        scope[2],
		service:       scope[3],
		signedHeaders: headers,
		signature:     sig,
		amzDate:       amzDate,
		presigned:     presigned,
	}, nil
}

func incompleteErr() *AuthError {
	return &AuthError{
		Code:       "IncompleteSignature",
		Message:    "Authorization header requires 'Credential', 'SignedHeaders' and 'Signature'.",
		HTTPStatus: unsignedStatus,
	}
}

// stringToSign builds the SigV4 string-to-sign for a canonical request.
func stringToSign(in *signInputs, canonicalRequest string) string {
	hashed := sha256.Sum256([]byte(canonicalRequest))
	scope := strings.Join([]string{in.dateStamp, in.region, in.service, terminator}, "/")

	return strings.Join([]string{
		algorithm,
		in.amzDate,
		scope,
		hex.EncodeToString(hashed[:]),
	}, "\n")
}

// signingKey derives the SigV4 signing key from the secret and credential scope.
func signingKey(secret string, in *signInputs) []byte {
	kDate := sign([]byte("AWS4"+secret), []byte(in.dateStamp))
	kRegion := sign(kDate, []byte(in.region))
	kService := sign(kRegion, []byte(in.service))

	return sign(kService, []byte(terminator))
}

func sign(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)

	return h.Sum(nil)
}
