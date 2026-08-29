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
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// dynamoProbe answers any DynamoDB JSON-RPC request with 200, so a test can
// observe whether the authorization gate let the request through to dispatch.
type dynamoProbe struct{}

func (dynamoProbe) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_20120810.")
}

func (dynamoProbe) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "ok")
}

// signedDynamoRequest builds and SigV4-signs a DynamoDB PutItem request whose
// body targets the given table.
func signedDynamoRequest(t *testing.T, url, table string, creds aws.Credentials) *http.Request {
	t.Helper()

	body := `{"TableName":"` + table + `","Item":{}}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(
		context.Background(), creds, req, hex.EncodeToString(sum[:]), "dynamodb", "us-east-1", time.Now(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	return req
}

// TestAuthzGateResourceScoped proves the authorization gate derives the target
// resource ARN from the request body so a resource-scoped policy allows the
// matching table and denies a non-matching one.
func TestAuthzGateResourceScoped(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "dyn"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:*",` +
		`"Resource":"arn:aws:dynamodb:us-east-1:123456789012:table/allowed"}]}`

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "dynpol", PolicyDocument: doc})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if err := cloud.IAM.AttachUserPolicy(ctx, "dyn", pol.ARN); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "dyn"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	srv := New(Drivers{IAM: cloud.IAM, AccountID: "123456789012", Region: "us-east-1", EnforceAuth: true})
	srv.Register(dynamoProbe{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	creds := aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}

	// Matching table: the resource-scoped Allow applies, so the request proceeds.
	resp, err := http.DefaultClient.Do(signedDynamoRequest(t, ts.URL+"/", "allowed", creds))
	if err != nil {
		t.Fatalf("do allowed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("matching table: want 200, got %d", resp.StatusCode)
	}

	// Non-matching table: the Allow no longer applies, so the gate denies (403).
	resp, err = http.DefaultClient.Do(signedDynamoRequest(t, ts.URL+"/", "denied", creds))
	if err != nil {
		t.Fatalf("do denied: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-matching table: want 403, got %d", resp.StatusCode)
	}
}
