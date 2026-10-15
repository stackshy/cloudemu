package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/stackshy/cloudemu/v2/config"
)

// presignWith presigns GET /bucket/key at signingTime. expires is the raw
// X-Amz-Expires value; omit leaves it out.
func presignWith(t *testing.T, expires string, omit bool, signingTime time.Time) *http.Request {
	t.Helper()

	base, err := http.NewRequest(http.MethodGet, "http://example.local/bucket/key", nil)
	if err != nil {
		t.Fatal(err)
	}

	if !omit {
		q := base.URL.Query()
		q.Set("X-Amz-Expires", expires)
		base.URL.RawQuery = q.Encode()
	}

	creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}

	uri, _, err := v4.NewSigner().PresignHTTP(base.Context(), creds, base, "UNSIGNED-PAYLOAD", "s3", "us-east-1", signingTime)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}

	r, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

// TestVerifyPresignedExpiresParameter requires X-Amz-Expires on a presigned
// URL, as a whole number of seconds from 1 to 604800. A URL without it, or
// with any other value, is rejected instead of never expiring.
func TestVerifyPresignedExpiresParameter(t *testing.T) {
	signingTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := config.NewFakeClock(signingTime.Add(time.Second))

	for _, tc := range []struct {
		name, value string
		omit        bool
		wantMsg     string
	}{
		{name: "missing", omit: true, wantMsg: "requires the X-Amz-Algorithm"},
		{name: "zero", value: "0", wantMsg: "greater than 0"},
		{name: "negative", value: "-5", wantMsg: "greater than 0"},
		{name: "not a number", value: "abc", wantMsg: "should be a number"},
		{name: "empty", value: "", wantMsg: "should be a number"},
		{name: "over a week", value: "604801", wantMsg: "less than a week"},
		{name: "overflow", value: "99999999999999999999", wantMsg: "less than a week"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, aerr := Verify(presignWith(t, tc.value, tc.omit, signingTime), nil, lookupOK(testSecret), clock)
			if aerr == nil || aerr.Code != "AuthorizationQueryParametersError" || aerr.HTTPStatus != http.StatusBadRequest ||
				!strings.Contains(aerr.Message, tc.wantMsg) {
				t.Fatalf("got %v, want 400 AuthorizationQueryParametersError containing %q", aerr, tc.wantMsg)
			}
		})
	}

	for _, v := range []string{"1", "604800"} {
		if _, aerr := Verify(presignWith(t, v, false, signingTime), nil, lookupOK(testSecret), clock); aerr != nil {
			t.Fatalf("X-Amz-Expires=%s rejected: %v", v, aerr)
		}
	}

	week := config.NewFakeClock(signingTime.Add(604800*time.Second + time.Second))
	if _, aerr := Verify(presignWith(t, "604800", false, signingTime), nil, lookupOK(testSecret), week); aerr == nil ||
		aerr.Message != "Request has expired" {
		t.Fatalf("a URL past X-Amz-Date+X-Amz-Expires: got %v, want Request has expired", aerr)
	}
}

// TestVerifyRejectsUnsignedAmzHeaders checks an S3 request carrying an
// x-amz-* header outside SignedHeaders is refused, for presigned URLs and
// header-signed requests, while x-amz-content-sha256 stays exempt.
func TestVerifyRejectsUnsignedAmzHeaders(t *testing.T) {
	signingTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := config.NewFakeClock(signingTime)

	wantUnsigned := func(t *testing.T, aerr *AuthError) {
		t.Helper()

		if aerr == nil || aerr.Code != "AccessDenied" || aerr.Message != "There were headers present in the request which were not signed" {
			t.Fatalf("got %v, want AccessDenied for unsigned headers", aerr)
		}
	}

	t.Run("presigned", func(t *testing.T) {
		r := presignWith(t, "300", false, signingTime)
		r.Header.Set("X-Amz-Copy-Source", "/other/secret")
		_, aerr := Verify(r, nil, lookupOK(testSecret), clock)
		wantUnsigned(t, aerr)

		r = presignWith(t, "300", false, signingTime)
		r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")

		if _, aerr := Verify(r, nil, lookupOK(testSecret), clock); aerr != nil {
			t.Fatalf("an unsigned x-amz-content-sha256 was rejected: %v", aerr)
		}
	})

	newSigned := func(service string) *http.Request {
		r, err := http.NewRequest(http.MethodPut, "http://example.local/bucket/key", strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}

		sum := sha256.Sum256([]byte("x"))
		r.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sum[:]))
		signAt(t, r, hex.EncodeToString(sum[:]), service, signingTime)

		return r
	}

	t.Run("header-signed", func(t *testing.T) {
		if _, aerr := Verify(newSigned("s3"), []byte("x"), lookupOK(testSecret), clock); aerr != nil {
			t.Fatalf("a fully signed request was rejected: %v", aerr)
		}

		r := newSigned("s3")
		r.Header.Set("X-Amz-Bypass-Governance-Retention", "true")
		_, aerr := Verify(r, []byte("x"), lookupOK(testSecret), clock)
		wantUnsigned(t, aerr)
	})

	t.Run("other services are not affected", func(t *testing.T) {
		r := newSigned("execute-api")
		r.Header.Set("X-Amz-Unsigned-Extra", "1")

		if _, aerr := Verify(r, []byte("x"), lookupOK(testSecret), clock); aerr != nil {
			t.Fatalf("non-S3 request with an unsigned x-amz header rejected: %v", aerr)
		}
	})
}
