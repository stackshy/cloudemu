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
	"github.com/stackshy/cloudemu/v2/server/authctx"
)

const (
	testAKID   = "AKIATESTKEY000000000"
	testSecret = "secret-test-value-000000000000000000"
)

// signReq signs r with the real aws-sdk-go-v2 v4 signer for the given service so
// the verifier is checked against the exact canonicalization the SDK produces.
func signReq(t *testing.T, r *http.Request, body []byte, service string) {
	t.Helper()

	sum := sha256.Sum256(body)
	creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}

	if err := v4.NewSigner().SignHTTP(
		r.Context(), creds, r, hex.EncodeToString(sum[:]), service, "us-east-1", time.Now(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

func lookupOK(secret string) LookupFunc {
	return func(id string) (string, authctx.Principal, bool) {
		return secret, authctx.Principal{AccessKeyID: id, UserName: "tester"}, true
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	// Non-S3 services: the aws-sdk-go-v2 signer double-encodes the path, so a
	// space in the path exercises that branch. (Real S3 clients disable path
	// escaping; that single-encode branch is proved end-to-end by the S3
	// compat test, which uses a real S3 client.)
	cases := []struct {
		service string
		url     string
	}{
		{"iam", "http://example.local/path/with%20space?b=2&a=one%20two"},
		{"execute-api", "http://example.local/prod/items?limit=10&next=a%20b"},
		{"s3", "http://example.local/bucket/key"}, // plain path: single==double encode
	}

	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			body := []byte("Action=ListUsers&Version=2010-05-08")
			r, err := http.NewRequest(http.MethodPost, c.url, strings.NewReader(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			signReq(t, r, body, c.service)

			p, aerr := Verify(r, body, lookupOK(testSecret), config.RealClock{})
			if aerr != nil {
				t.Fatalf("Verify: %v", aerr)
			}
			if p.AccessKeyID != testAKID || p.UserName != "tester" {
				t.Fatalf("unexpected principal: %+v", p)
			}
		})
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	body := []byte("x")
	r, _ := http.NewRequest(http.MethodPost, "http://example.local/", strings.NewReader("x"))
	signReq(t, r, body, "iam")

	_, aerr := Verify(r, body, lookupOK("secret-different"), config.RealClock{})
	if aerr == nil || aerr.Code != "SignatureDoesNotMatch" || aerr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("want SignatureDoesNotMatch/403, got %v", aerr)
	}
}

func TestVerifyUnknownKey(t *testing.T) {
	body := []byte("x")
	r, _ := http.NewRequest(http.MethodPost, "http://example.local/", strings.NewReader("x"))
	signReq(t, r, body, "iam")

	lookup := func(string) (string, authctx.Principal, bool) { return "", authctx.Principal{}, false }
	_, aerr := Verify(r, body, lookup, config.RealClock{})
	if aerr == nil || aerr.Code != "InvalidClientTokenId" {
		t.Fatalf("want InvalidClientTokenId, got %v", aerr)
	}
}

func TestVerifyMissingAuth(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://example.local/", nil)

	_, aerr := Verify(r, nil, lookupOK(testSecret), config.RealClock{})
	if aerr == nil || aerr.Code != "MissingAuthenticationToken" {
		t.Fatalf("want MissingAuthenticationToken, got %v", aerr)
	}
}

func TestVerifyMalformedCredential(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://example.local/", nil)
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=AKIA/only, SignedHeaders=host, Signature=deadbeef")
	r.Header.Set("X-Amz-Date", "20260101T000000Z")

	_, aerr := Verify(r, nil, lookupOK(testSecret), config.RealClock{})
	if aerr == nil || aerr.Code != "IncompleteSignature" {
		t.Fatalf("want IncompleteSignature, got %v", aerr)
	}
}

func TestAccessKeyID(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://example.local/", nil)
	if got := AccessKeyID(r); got != "" {
		t.Fatalf("unsigned request: want empty AKID, got %q", got)
	}

	signReq(t, r, nil, "iam")
	if got := AccessKeyID(r); got != testAKID {
		t.Fatalf("want %q, got %q", testAKID, got)
	}
}

func TestURIEncode(t *testing.T) {
	cases := []struct {
		in          string
		encodeSlash bool
		want        string
	}{
		{"/a/b", false, "/a/b"},
		{"/a/b", true, "%2Fa%2Fb"},
		{"a b", true, "a%20b"},
		{"tilde~-_.09AZ", true, "tilde~-_.09AZ"},
		{"k=v&x", true, "k%3Dv%26x"},
	}

	for _, c := range cases {
		if got := uriEncode(c.in, c.encodeSlash); got != c.want {
			t.Errorf("uriEncode(%q,%v)=%q want %q", c.in, c.encodeSlash, got, c.want)
		}
	}
}
