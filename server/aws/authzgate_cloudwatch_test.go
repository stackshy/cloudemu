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

// cloudWatchJSONProbe answers any CloudWatch awsJson1_0 request with 200.
type cloudWatchJSONProbe struct{}

func (cloudWatchJSONProbe) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), "GraniteServiceVersion20100801.")
}

func (cloudWatchJSONProbe) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "{}")
}

func signedCloudWatchJSONRequest(t *testing.T, url, op string, creds aws.Credentials) *http.Request {
	t.Helper()

	body := `{}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Amz-Target", "GraniteServiceVersion20100801."+op)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(
		context.Background(), creds, req, hex.EncodeToString(sum[:]), "monitoring", "us-east-1", time.Now(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	return req
}

// TestAuthzGateCloudWatchJSON checks that the authorization gate maps a
// GraniteServiceVersion20100801 target to the cloudwatch IAM service, so an
// allow on cloudwatch:* lets boto3's CloudWatch calls through and an explicit
// deny still blocks its action.
func TestAuthzGateCloudWatchJSON(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "cw"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	doc := `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"cloudwatch:*","Resource":"*"},` +
		`{"Effect":"Deny","Action":"cloudwatch:DeleteAlarms","Resource":"*"}]}`

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "cwpol", PolicyDocument: doc})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if err := cloud.IAM.AttachUserPolicy(ctx, "cw", pol.ARN); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "cw"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	srv := New(Drivers{IAM: cloud.IAM, AccountID: "123456789012", Region: "us-east-1", EnforceAuth: true})
	srv.Register(cloudWatchJSONProbe{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	creds := aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}

	for op, want := range map[string]int{
		"DescribeAlarms": http.StatusOK,
		"PutMetricData":  http.StatusOK,
		"DeleteAlarms":   http.StatusForbidden,
	} {
		resp, err := http.DefaultClient.Do(signedCloudWatchJSONRequest(t, ts.URL+"/", op, creds))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}

		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != want {
			t.Fatalf("%s: status %d, want %d: %s", op, resp.StatusCode, want, raw)
		}
	}
}
