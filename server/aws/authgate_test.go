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

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server/authctx"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// principalProbe is a server.Handler that answers /_whoami by writing the
// authenticated principal's user name from the request context, so a test can
// assert the SigV4 gate resolved and propagated it.
type principalProbe struct{}

func (principalProbe) Matches(r *http.Request) bool { return r.URL.Path == "/_whoami" }

func (principalProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := authctx.PrincipalFrom(r.Context())
	if !ok {
		http.Error(w, "no principal", http.StatusInternalServerError)
		return
	}

	_, _ = io.WriteString(w, p.UserName+"|"+p.ARN)
}

// TestAuthGatePropagatesPrincipal signs a request with the real aws-sdk-go-v2
// v4 signer using a registered access key, and asserts the gate verified it and
// placed the resolved principal on the request context that the handler sees.
func TestAuthGatePropagatesPrincipal(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "carol"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "carol"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	srv := New(Drivers{IAM: cloud.IAM, EnforceAuth: true})
	srv.Register(principalProbe{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/_whoami", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	emptyHash := sha256.Sum256(nil)
	creds := aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}

	if err := v4.NewSigner().SignHTTP(
		ctx, creds, req, hex.EncodeToString(emptyHash[:]), "iam", "us-east-1", time.Now(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	if got := string(body); !strings.HasPrefix(got, "carol|") ||
		!strings.Contains(got, ":user/carol") {
		t.Fatalf("handler did not see the resolved principal: %q", got)
	}
}
