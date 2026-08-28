package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const tempTestAccount = "123456789012"

// signTemp signs r with the given temporary credentials at signingTime using the
// real v4 signer, so the request carries the ASIA credential's SigV4 material and
// X-Amz-Security-Token exactly as an SDK would produce.
func signTemp(t *testing.T, r *http.Request, akid, secret, token string, signingTime time.Time) {
	t.Helper()

	creds := aws.Credentials{AccessKeyID: akid, SecretAccessKey: secret, SessionToken: token}
	if err := v4.NewSigner().SignHTTP(
		r.Context(), creds, r, hex.EncodeToString(func() []byte { s := sha256.Sum256(nil); return s[:] }()),
		"iam", "us-east-1", signingTime,
	); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// TestVerifyTempCredential exercises the STS temporary-credential branch of the
// gate directly: a signature made with the STS-issued secret verifies, a forged
// secret is rejected, an unknown key is rejected, and an expired session is
// rejected — all deterministically on a FakeClock.
func TestVerifyTempCredential(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clock := config.NewFakeClock(now)
	store := stssrv.NewSessionStore(clock)

	issued := store.Mint(time.Hour) // Expiration = now + 1h

	newReq := func() *http.Request {
		r, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			"http://example.local/", strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	t.Run("valid", func(t *testing.T) {
		r := newReq()
		signTemp(t, r, issued.AccessKeyID, issued.SecretAccessKey, issued.SessionToken, now)

		p, aerr := verifyTempCredential(r, nil, issued.AccessKeyID, tempTestAccount, store, clock)
		if aerr != nil {
			t.Fatalf("valid temp credential rejected: %v", aerr)
		}
		if p.AccessKeyID != issued.AccessKeyID {
			t.Fatalf("principal AccessKeyID = %q, want %q", p.AccessKeyID, issued.AccessKeyID)
		}
	})

	t.Run("forged-secret", func(t *testing.T) {
		r := newReq()
		signTemp(t, r, issued.AccessKeyID, "forged-secret-000000000000000000000000", issued.SessionToken, now)

		_, aerr := verifyTempCredential(r, nil, issued.AccessKeyID, tempTestAccount, store, clock)
		if aerr == nil || aerr.Code != "SignatureDoesNotMatch" {
			t.Fatalf("want SignatureDoesNotMatch, got %v", aerr)
		}
	})

	t.Run("unknown-key", func(t *testing.T) {
		r := newReq()
		signTemp(t, r, "ASIAUNKNOWN0000000000", issued.SecretAccessKey, issued.SessionToken, now)

		_, aerr := verifyTempCredential(r, nil, "ASIAUNKNOWN0000000000", tempTestAccount, store, clock)
		if aerr == nil || aerr.Code != "InvalidClientTokenId" {
			t.Fatalf("want InvalidClientTokenId, got %v", aerr)
		}
	})

	t.Run("no-store-fails-closed", func(t *testing.T) {
		r := newReq()
		signTemp(t, r, issued.AccessKeyID, issued.SecretAccessKey, issued.SessionToken, now)

		_, aerr := verifyTempCredential(r, nil, issued.AccessKeyID, tempTestAccount, nil, clock)
		if aerr == nil || aerr.Code != "InvalidClientTokenId" {
			t.Fatalf("want InvalidClientTokenId (fail closed), got %v", aerr)
		}
	})

	t.Run("expired-session", func(t *testing.T) {
		shortClock := config.NewFakeClock(now)
		shortStore := stssrv.NewSessionStore(shortClock)
		short := shortStore.Mint(15 * time.Minute) // Expiration = now + 15m

		r := newReq()
		signTemp(t, r, short.AccessKeyID, short.SecretAccessKey, short.SessionToken, now)

		shortClock.Advance(time.Hour) // now past expiration
		_, aerr := verifyTempCredential(r, nil, short.AccessKeyID, tempTestAccount, shortStore, shortClock)
		if aerr == nil || aerr.Code != "ExpiredToken" {
			t.Fatalf("want ExpiredToken, got %v", aerr)
		}
	})
}

// TestAuthGateVerifiesAssumedRoleCredential is the real-user end-to-end flow: a
// registered AKIA key assumes a role over the SDK, then the returned ASIA
// credential is used to make an authenticated request against the same server —
// proving STS and the gate share the session store. A tampered secret is
// rejected. Uses the real clock so the SDK's own signing time is fresh.
func TestAuthGateVerifiesAssumedRoleCredential(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "dave"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "dave"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	// The wired IAM enforces the target role's trust policy, so create AppRole
	// trusting the account root (the caller principal STS evaluates against).
	if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{
		Name: "AppRole",
		AssumeRolePolicyDoc: `{"Statement":[{"Effect":"Allow",` +
			`"Principal":{"AWS":"arn:aws:iam::` + tempTestAccount + `:root"},"Action":"sts:AssumeRole"}]}`,
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	srv := New(Drivers{IAM: cloud.IAM, STS: true, AccountID: tempTestAccount, EnforceAuth: true})
	srv.Register(principalProbe{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Assume a role with the long-term key; the SDK signs the AssumeRole call.
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(ak.AccessKeyID, ak.SecretAccessKey, ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	stsc := awssts.NewFromConfig(cfg, func(o *awssts.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	out, err := stsc.AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::123456789012:role/AppRole"),
		RoleSessionName: aws.String("e2e"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	tempAKID := aws.ToString(out.Credentials.AccessKeyId)
	tempSecret := aws.ToString(out.Credentials.SecretAccessKey)
	tempToken := aws.ToString(out.Credentials.SessionToken)

	if !strings.HasPrefix(tempAKID, "ASIA") {
		t.Fatalf("temp AccessKeyId = %q, want ASIA prefix", tempAKID)
	}

	// Use the assumed-role credentials to make an authenticated request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/_whoami", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	signTemp(t, req, tempAKID, tempSecret, tempToken, time.Now())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assumed-role request: want 200, got %d: %s", resp.StatusCode, body)
	}

	// A tampered secret on the same ASIA key id must be rejected.
	forged, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/_whoami", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	signTemp(t, forged, tempAKID, tempSecret+"tampered", tempToken, time.Now())

	fresp, err := http.DefaultClient.Do(forged)
	if err != nil {
		t.Fatalf("do forged: %v", err)
	}
	_ = fresp.Body.Close()

	if fresp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged assumed-role request: want 403, got %d", fresp.StatusCode)
	}
}

// TestSTSCredentialsGatedByEnforceAuth proves the session store — and thus the
// unique/verifiable credentials — appear only under EnforceAuth. With it off,
// AssumeRole returns the fixed synthetic credential the emulator always has, so
// the default behavior is unchanged.
func TestSTSCredentialsGatedByEnforceAuth(t *testing.T) {
	srv := New(Drivers{STS: true, AccountID: tempTestAccount})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	stsc := awssts.NewFromConfig(cfg, func(o *awssts.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	out, err := stsc.AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::123456789012:role/AppRole"),
		RoleSessionName: aws.String("off"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	if got := aws.ToString(out.Credentials.AccessKeyId); got != "ASIACLOUDEMU000000000" {
		t.Fatalf("EnforceAuth off: AccessKeyId = %q, want fixed synthetic value", got)
	}
}
