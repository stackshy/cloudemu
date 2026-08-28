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
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/authctx"
)

const (
	algorithm      = "AWS4-HMAC-SHA256"
	terminator     = "aws4_request"
	unsignedStatus = http.StatusForbidden
	// credScopeParts is AKID/DATE/REGION/SERVICE/aws4_request.
	credScopeParts = 5
	// maxClockSkew is the tolerance between a signed request's X-Amz-Date and the
	// server clock for the Authorization-header form (real AWS allows 5 minutes).
	maxClockSkew = 5 * time.Minute
	// amzDateFormat is the ISO-8601 basic timestamp the SDK puts in X-Amz-Date.
	amzDateFormat = "20060102T150405Z"
	// sha256HexLen is the length of a hex-encoded SHA-256 digest; an
	// x-amz-content-sha256 of exactly this many hex chars is a real body hash
	// (not one of the UNSIGNED-PAYLOAD / STREAMING-* sentinels).
	sha256HexLen = 64
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
	amzDate       string        // full timestamp used in the string-to-sign
	expires       time.Duration // presigned validity window (X-Amz-Expires); 0 if absent
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
// typed AuthError. body is the buffered request body (the gate reads and
// restores it). clock drives timestamp-expiry evaluation, so a FakeClock makes
// it deterministic in tests.
//
// Beyond the signature it enforces two body/time bindings a captured-but-valid
// signature would otherwise slip past:
//   - x-amz-content-sha256 body binding: when the header is a real body hash
//     (not a sentinel), the actual body is re-hashed and a mismatch is rejected,
//     so a body swap that keeps the signed header fails.
//   - timestamp expiry / clock skew: an X-Amz-Date outside the allowed window
//     (5-minute skew for header-signed requests, X-Amz-Expires for presigned
//     URLs) is rejected, so a captured signature cannot be replayed later.
func Verify(r *http.Request, body []byte, lookup LookupFunc, clock config.Clock) (authctx.Principal, *AuthError) {
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

	if aerr := checkContentSHA256(r, body); aerr != nil {
		return authctx.Principal{}, aerr
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

	if aerr := checkExpiry(&in, clock); aerr != nil {
		return authctx.Principal{}, aerr
	}

	return principal, nil
}

// checkContentSHA256 re-binds the S3 x-amz-content-sha256 header to the actual
// body. The header value is part of the signed canonical request, so a captured
// request whose body is swapped but whose header is left intact still produces a
// matching signature; re-hashing the body and comparing closes that gap. The
// UNSIGNED-PAYLOAD and STREAMING-* sentinels carry no body hash, so they are
// skipped (recognized by not being a 64-char hex digest). Absent header: skip.
func checkContentSHA256(r *http.Request, body []byte) *AuthError {
	v := r.Header.Get("X-Amz-Content-Sha256")
	if !isHexSHA256(v) {
		return nil
	}

	sum := sha256.Sum256(body)
	if !strings.EqualFold(v, hex.EncodeToString(sum[:])) {
		return &AuthError{
			Code:       "XAmzContentSHA256Mismatch",
			Message:    "The provided 'x-amz-content-sha256' header does not match what was computed.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	return nil
}

// isHexSHA256 reports whether v is exactly a hex-encoded SHA-256 digest (and so
// a real body hash rather than a sentinel such as UNSIGNED-PAYLOAD).
func isHexSHA256(v string) bool {
	if len(v) != sha256HexLen {
		return false
	}

	for i := 0; i < len(v); i++ {
		c := v[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	return true
}

// checkExpiry rejects a request whose signing timestamp is outside the allowed
// window. Header-signed requests must be within maxClockSkew of now in either
// direction; presigned URLs must be within their X-Amz-Expires window (and not
// dated in the future beyond the skew). An unparseable date is not evaluated
// (the signature already matched), which never happens for real SDK requests.
func checkExpiry(in *signInputs, clock config.Clock) *AuthError {
	signed, ok := parseAmzDate(in.amzDate)
	if !ok {
		return nil
	}

	now := clock.Now().UTC()

	if in.presigned {
		if in.expires > 0 && now.After(signed.Add(in.expires)) {
			return expiredErr()
		}

		if now.Before(signed.Add(-maxClockSkew)) {
			return skewErr()
		}

		return nil
	}

	diff := now.Sub(signed)
	if diff < 0 {
		diff = -diff
	}

	if diff > maxClockSkew {
		return skewErr()
	}

	return nil
}

// parseAmzDate parses the X-Amz-Date value, accepting the ISO-8601 basic form
// the SDK uses and the RFC1123 form a Date-header fallback may carry.
func parseAmzDate(s string) (time.Time, bool) {
	if t, err := time.Parse(amzDateFormat, s); err == nil {
		return t.UTC(), true
	}

	if t, err := time.Parse(time.RFC1123, s); err == nil {
		return t.UTC(), true
	}

	return time.Time{}, false
}

func expiredErr() *AuthError {
	return &AuthError{
		Code:       "AccessDenied",
		Message:    "Request has expired",
		HTTPStatus: unsignedStatus,
	}
}

func skewErr() *AuthError {
	return &AuthError{
		Code:       "RequestTimeTooSkewed",
		Message:    "The difference between the request time and the current time is too large.",
		HTTPStatus: unsignedStatus,
	}
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

	in, aerr := buildInputs(
		q.Get("X-Amz-Credential"),
		q.Get("X-Amz-SignedHeaders"),
		q.Get("X-Amz-Signature"),
		q.Get("X-Amz-Date"),
		true,
	)
	if aerr != nil {
		return in, aerr
	}

	if v := q.Get("X-Amz-Expires"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			in.expires = time.Duration(secs) * time.Second
		}
	}

	return in, nil
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
