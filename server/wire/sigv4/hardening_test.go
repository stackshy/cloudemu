package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/stackshy/cloudemu/v2/config"
)

// signAt signs r with the real v4 signer at signingTime, using the given payload
// hash verbatim (so a caller can sign UNSIGNED-PAYLOAD or a real body hash).
func signAt(t *testing.T, r *http.Request, payloadHash, service string, signingTime time.Time) {
	t.Helper()

	creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}
	if err := v4.NewSigner().SignHTTP(
		r.Context(), creds, r, payloadHash, service, "us-east-1", signingTime,
	); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// TestVerifyClockSkew proves a header-signed request outside the 5-minute skew
// window is rejected while a fresh one (evaluated at signing time) passes.
func TestVerifyClockSkew(t *testing.T) {
	signingTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	body := []byte("Action=ListUsers&Version=2010-05-08")

	newSigned := func() *http.Request {
		r, err := http.NewRequest(http.MethodPost, "http://example.local/", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		sum := sha256.Sum256(body)
		signAt(t, r, hex.EncodeToString(sum[:]), "iam", signingTime)

		return r
	}

	// Fresh: clock at signing time -> passes.
	if _, aerr := Verify(newSigned(), body, lookupOK(testSecret), config.NewFakeClock(signingTime)); aerr != nil {
		t.Fatalf("fresh request rejected: %v", aerr)
	}

	// Skew exceeded (10 minutes later) -> RequestTimeTooSkewed.
	late := config.NewFakeClock(signingTime.Add(10 * time.Minute))
	if _, aerr := Verify(newSigned(), body, lookupOK(testSecret), late); aerr == nil ||
		aerr.Code != "RequestTimeTooSkewed" {
		t.Fatalf("want RequestTimeTooSkewed, got %v", aerr)
	}

	// Skew exceeded in the past direction too.
	early := config.NewFakeClock(signingTime.Add(-10 * time.Minute))
	if _, aerr := Verify(newSigned(), body, lookupOK(testSecret), early); aerr == nil ||
		aerr.Code != "RequestTimeTooSkewed" {
		t.Fatalf("want RequestTimeTooSkewed (past), got %v", aerr)
	}
}

// TestVerifyPresignedExpiry proves a presigned URL is accepted inside its
// X-Amz-Expires window and rejected once it has elapsed.
func TestVerifyPresignedExpiry(t *testing.T) {
	signingTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	const expires = 900 * time.Second

	presign := func() *http.Request {
		base, err := http.NewRequest(http.MethodGet, "http://example.local/bucket/key", nil)
		if err != nil {
			t.Fatal(err)
		}

		q := base.URL.Query()
		q.Set("X-Amz-Expires", strconv.Itoa(int(expires.Seconds())))
		base.URL.RawQuery = q.Encode()

		creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}
		uri, _, err := v4.NewSigner().PresignHTTP(
			base.Context(), creds, base, "UNSIGNED-PAYLOAD", "s3", "us-east-1", signingTime,
		)
		if err != nil {
			t.Fatalf("presign: %v", err)
		}

		r, err := http.NewRequest(http.MethodGet, uri, nil)
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	// Inside the window -> passes.
	fresh := config.NewFakeClock(signingTime.Add(5 * time.Minute))
	if _, aerr := Verify(presign(), nil, lookupOK(testSecret), fresh); aerr != nil {
		t.Fatalf("presigned URL inside window rejected: %v", aerr)
	}

	// Past the window -> expired.
	expired := config.NewFakeClock(signingTime.Add(expires + time.Minute))
	if _, aerr := Verify(presign(), nil, lookupOK(testSecret), expired); aerr == nil ||
		aerr.Code != "AccessDenied" {
		t.Fatalf("want AccessDenied (expired), got %v", aerr)
	}
}

// TestVerifyContentSHA256BodySwap proves a request whose body is swapped after
// signing but whose signed x-amz-content-sha256 header is kept is rejected, and
// that a genuine body still verifies.
func TestVerifyContentSHA256BodySwap(t *testing.T) {
	signingTime := time.Now()
	original := []byte(`{"amount":1}`)

	newSigned := func() *http.Request {
		r, err := http.NewRequest(http.MethodPost, "http://example.local/bucket/key",
			strings.NewReader(string(original)))
		if err != nil {
			t.Fatal(err)
		}

		// A real S3 client sends x-amz-content-sha256 as a signed header, so the
		// canonical request binds to the header value, not the live body — the
		// exact condition a body swap exploits.
		sum := sha256.Sum256(original)
		r.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sum[:]))
		signAt(t, r, hex.EncodeToString(sum[:]), "s3", signingTime)

		return r
	}

	// Genuine body: header matches -> passes.
	if _, aerr := Verify(newSigned(), original, lookupOK(testSecret), config.RealClock{}); aerr != nil {
		t.Fatalf("genuine body rejected: %v", aerr)
	}

	// Swapped body, header (and signature) preserved -> mismatch.
	swapped := []byte(`{"amount":9999}`)
	if _, aerr := Verify(newSigned(), swapped, lookupOK(testSecret), config.RealClock{}); aerr == nil ||
		aerr.Code != "XAmzContentSHA256Mismatch" {
		t.Fatalf("want XAmzContentSHA256Mismatch, got %v", aerr)
	}
}

// TestVerifyUnsignedPayloadSkipsBodyBinding proves that with the UNSIGNED-PAYLOAD
// sentinel the body is not re-hashed, so a request still verifies regardless of
// the body bytes presented (the SDK's legitimate unsigned-payload mode).
func TestVerifyUnsignedPayloadSkipsBodyBinding(t *testing.T) {
	r, err := http.NewRequest(http.MethodPut, "http://example.local/bucket/key",
		strings.NewReader("streamed-body"))
	if err != nil {
		t.Fatal(err)
	}

	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	signAt(t, r, "UNSIGNED-PAYLOAD", "s3", time.Now())

	// Present a different body than was streamed: UNSIGNED-PAYLOAD means the body
	// is not bound, so verification still passes.
	if _, aerr := Verify(r, []byte("anything-else"), lookupOK(testSecret), config.RealClock{}); aerr != nil {
		t.Fatalf("UNSIGNED-PAYLOAD request rejected: %v", aerr)
	}
}

// TestIsHexSHA256 covers the sentinel/real-hash discriminator.
func TestIsHexSHA256(t *testing.T) {
	real := hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte("x")); return s[:] }())

	cases := []struct {
		in   string
		want bool
	}{
		{real, true},
		{strings.ToUpper(real), true},
		{"UNSIGNED-PAYLOAD", false},
		{"STREAMING-AWS4-HMAC-SHA256-PAYLOAD", false},
		{"", false},
		{real[:63], false},
		{real[:63] + "g", false},
	}

	for _, c := range cases {
		if got := isHexSHA256(c.in); got != c.want {
			t.Errorf("isHexSHA256(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
