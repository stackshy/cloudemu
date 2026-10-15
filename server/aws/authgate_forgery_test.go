package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	stssrv "github.com/stackshy/cloudemu/v2/server/aws/sts"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// signedWhoami sends a /_whoami request signed with the given long-term
// credentials and returns the status code.
func signedWhoami(t *testing.T, url, akid, secret string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/_whoami", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	emptyHash := sha256.Sum256(nil)
	creds := aws.Credentials{AccessKeyID: akid, SecretAccessKey: secret}

	if err := v4.NewSigner().SignHTTP(
		req.Context(), creds, req, hex.EncodeToString(emptyHash[:]), "iam", "us-east-1", time.Now(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	return resp.StatusCode
}

// TestAuthGateRejectsCounterGuessedSecret replays the forgery an attacker could
// mount when access-key secrets were built from the shared id counter: knowing
// only the access key id, sign with every "secret-%08x" near the counter's
// current value. Under EnforceAuth every guess must be rejected, while the real
// secret still works.
func TestAuthGateRejectsCounterGuessedSecret(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "victim"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "victim"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	probe, err := strconv.ParseUint(idgen.GenerateID(""), 16, 64)
	if err != nil {
		t.Fatalf("parse probe id: %v", err)
	}

	srv := New(Drivers{IAM: cloud.IAM, EnforceAuth: true})
	srv.Register(principalProbe{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	if got := signedWhoami(t, ts.URL, ak.AccessKeyID, ak.SecretAccessKey); got != http.StatusOK {
		t.Fatalf("real secret: want 200, got %d", got)
	}

	const window = 32

	for n := probe - min(window, probe); n <= probe; n++ {
		guess := fmt.Sprintf("secret-%08x", n)
		if got := signedWhoami(t, ts.URL, ak.AccessKeyID, guess); got != http.StatusForbidden {
			t.Fatalf("forged with guessed secret %q: want 403, got %d", guess, got)
		}
	}
}

// TestVerifyTempCredentialBindsSessionToken checks that an ASIA credential is
// accepted only with the session token STS issued alongside it.
func TestVerifyTempCredentialBindsSessionToken(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clock := config.NewFakeClock(now)
	store := stssrv.NewSessionStore(clock)

	issued, err := store.Mint(time.Hour, stssrv.SessionOwner{})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	cases := []struct {
		name, token string
		wantOK      bool
	}{
		{"issued token", issued.SessionToken, true},
		{"missing token", "", false},
		{"wrong token", issued.SessionToken + "x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				"http://example.local/", strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}

			signTemp(t, r, issued.AccessKeyID, issued.SecretAccessKey, tc.token, now)

			_, _, aerr := verifyTempCredential(r, nil, issued.AccessKeyID, tempTestAccount, store, clock)
			if (aerr == nil) != tc.wantOK {
				t.Fatalf("verifyTempCredential error = %v, want ok=%v", aerr, tc.wantOK)
			}
		})
	}
}
