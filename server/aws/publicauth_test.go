package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// enforcedServer starts the full AWS wire server with --enforce-auth on.
func enforcedServer(t *testing.T) (*httptest.Server, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	d := DriversFrom(cloud)
	d.EnforceAuth = true

	ts := httptest.NewServer(New(d))
	t.Cleanup(ts.Close)

	return ts, cloud
}

type rawReq struct {
	method, path, host, body string
	header                   map[string]string
}

func doRaw(t *testing.T, ts *httptest.Server, rq rawReq) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), rq.method, ts.URL+rq.path, strings.NewReader(rq.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if rq.host != "" {
		req.Host = rq.host
	}

	for k, v := range rq.header {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

const (
	formCT     = "application/x-www-form-urlencoded"
	amzJSON11  = "application/x-amz-json-1.1"
	missingTok = "MissingAuthenticationToken"
	idpTarget  = "AWSCognitoIdentityProviderService."
	identTgt   = "AWSCognitoIdentityService."
	lambdaPath = "/2015-03-31/functions"
	execHost   = "abc123.execute-api.us-east-1.amazonaws.com"

	// defaultTestAccount is the account cloudemu.NewAWS() uses by default.
	defaultTestAccount = "123456789012"
)

func jsonRPC(target, body string) rawReq {
	return rawReq{
		method: http.MethodPost, path: "/", body: body,
		header: map[string]string{"X-Amz-Target": target, "Content-Type": amzJSON11},
	}
}

func queryForm(body string) rawReq {
	return rawReq{method: http.MethodPost, path: "/", body: body, header: map[string]string{"Content-Type": formCT}}
}

// TestEnforcedGateAdmitsUnsignedPublicOps sends unsigned requests for operations
// AWS serves without SigV4 and asserts each reaches its handler instead of the
// gate's 403 MissingAuthenticationToken.
func TestEnforcedGateAdmitsUnsignedPublicOps(t *testing.T) {
	ts, _ := enforcedServer(t)

	cases := []struct {
		name string
		req  rawReq
		want int
	}{
		// Cognito user-pool public ops are not routed yet, so the handler answers
		// UnknownOperationException (400): the point is the gate let them through.
		{"cognito-idp InitiateAuth", jsonRPC(idpTarget+"InitiateAuth", `{}`), http.StatusBadRequest},
		{"cognito-idp SignUp", jsonRPC(idpTarget+"SignUp", `{}`), http.StatusBadRequest},
		{"cognito-idp RespondToAuthChallenge", jsonRPC(idpTarget+"RespondToAuthChallenge", `{}`), http.StatusBadRequest},
		// An unknown API reaches the API Gateway data plane, which answers like
		// real API Gateway: 403 {"message":"Missing Authentication Token"}. That
		// body differs from the gate's MissingAuthenticationToken error.
		{"execute-api host", rawReq{method: http.MethodGet, path: "/prod/pets", host: execHost}, http.StatusForbidden},
		{"execute-api path", rawReq{method: http.MethodGet, path: "/restapis/abc123/prod/_user_request_/pets"}, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doRaw(t, ts, tc.req)
			if strings.Contains(body, missingTok) || status != tc.want {
				t.Fatalf("status %d (want %d), body %s", status, tc.want, body)
			}
		})
	}
}

// signedJSONRPC sends a SigV4-signed JSON-RPC call with creds (a session token
// in creds is sent as X-Amz-Security-Token) and returns the status and __type.
func signedJSONRPC(t *testing.T, ts *httptest.Server, creds aws.Credentials, service, target string) (int, string) {
	t.Helper()

	ctx := context.Background()
	body := `{}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Content-Type", amzJSON11)

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), service, "us-east-1", time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	var e struct {
		Type string `json:"__type"`
	}

	_ = json.Unmarshal(b, &e)

	return resp.StatusCode, e.Type
}

// userWithPolicy creates an IAM user, attaches doc as a managed policy when it
// is non-empty, and returns a long-term key for it.
func userWithPolicy(t *testing.T, cloud *awsprovider.Provider, name, doc string) aws.Credentials {
	t.Helper()

	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: name}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if doc != "" {
		pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: name + "-policy", PolicyDocument: doc})
		if err != nil {
			t.Fatalf("CreatePolicy: %v", err)
		}

		if err := cloud.IAM.AttachUserPolicy(ctx, name, pol.ARN); err != nil {
			t.Fatalf("AttachUserPolicy: %v", err)
		}
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: name})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	return aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}
}

const (
	allowDynamo = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:*","Resource":"*"}]}`
	allowSQS    = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`
	listTables  = "DynamoDB_20120810.ListTables"
	listQueues  = "AmazonSQS.ListQueues"
	accessDeny  = "AccessDeniedException"
)

// TestAuthzSkipsPublicOps: a signed caller whose IAM policy allows only
// DynamoDB is still served a public Cognito operation (IAM never governs it),
// while a private Cognito operation is denied by the authorization gate.
func TestAuthzSkipsPublicOps(t *testing.T) {
	ts, cloud := enforcedServer(t)
	creds := userWithPolicy(t, cloud, "dynonly", allowDynamo)

	if status, typ := signedJSONRPC(t, ts, creds, "cognito-idp", idpTarget+"InitiateAuth"); status == http.StatusForbidden {
		t.Fatalf("public InitiateAuth denied: %d %s", status, typ)
	}

	if status, typ := signedJSONRPC(t, ts, creds, "cognito-idp", idpTarget+"ListUserPools"); status != http.StatusForbidden ||
		typ != accessDeny {
		t.Fatalf("private ListUserPools: %d %s, want 403 %s", status, typ, accessDeny)
	}
}

func stsClient(ts *httptest.Server, creds aws.Credentials) *awssts.Client {
	return awssts.New(awssts.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return creds, nil }),
	})
}

func sessionCreds(c *ststypes.Credentials) aws.Credentials {
	return aws.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
	}
}

// signedAssumeWebIdentity sends a SigV4-signed AssumeRoleWithWebIdentity for
// roleArn and returns the session credentials from the XML response.
func signedAssumeWebIdentity(t *testing.T, ts *httptest.Server, creds aws.Credentials, roleArn string) aws.Credentials {
	t.Helper()

	ctx := context.Background()
	body := url.Values{
		"Action": {"AssumeRoleWithWebIdentity"}, "Version": {"2011-06-15"}, "RoleArn": {roleArn},
		"RoleSessionName": {"s"}, "WebIdentityToken": {"junk"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", formCT)

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), "sts", "us-east-1", time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Result struct {
			Credentials struct {
				AccessKeyID     string `xml:"AccessKeyId"`
				SecretAccessKey string `xml:"SecretAccessKey"`
				SessionToken    string `xml:"SessionToken"`
			} `xml:"Credentials"`
		} `xml:"AssumeRoleWithWebIdentityResult"`
	}

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || xml.Unmarshal(raw, &out) != nil {
		t.Fatalf("signed AssumeRoleWithWebIdentity: %d %s", resp.StatusCode, raw)
	}

	c := out.Result.Credentials

	return aws.Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}
}

// TestSessionCredentialsAreAuthorized proves an STS session is authorized as
// its owner. A role session gets exactly its role's policies: a role that does
// not exist, or has no allowing policy, is denied. A GetSessionToken session
// gets the calling user's policies.
func TestSessionCredentialsAreAuthorized(t *testing.T) {
	ts, cloud := enforcedServer(t)
	ctx := context.Background()

	// "boot" has no policies, so its own key is unrestricted (bootstrap).
	bootCreds := userWithPolicy(t, cloud, "boot", "")
	boot := stsClient(ts, bootCreds)

	trust := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::` + defaultTestAccount +
		`:root"},"Action":"sts:AssumeRole"}]}`

	for _, role := range []string{"noperm", "dynrole"} {
		if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: role, AssumeRolePolicyDoc: trust}); err != nil {
			t.Fatalf("CreateRole %s: %v", role, err)
		}
	}

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "dynrole-policy", PolicyDocument: allowDynamo})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if err := cloud.IAM.AttachRolePolicy(ctx, "dynrole", pol.ARN); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	assume := func(role string) aws.Credentials {
		out, err := boot.AssumeRole(ctx, &awssts.AssumeRoleInput{
			RoleArn: aws.String("arn:aws:iam::" + defaultTestAccount + ":role/" + role), RoleSessionName: aws.String("s"),
		})
		if err != nil {
			t.Fatalf("AssumeRole %s: %v", role, err)
		}

		return sessionCreds(out.Credentials)
	}

	// The SDK always sends AssumeRoleWithWebIdentity unsigned (noAuth), so sign
	// it by hand: an authenticated caller asking for a role that does not exist.
	web := signedAssumeWebIdentity(t, ts, bootCreds, "arn:aws:iam::"+defaultTestAccount+":role/nonexistent")

	sessionFor := func(user string, doc string) aws.Credentials {
		out, err := stsClient(ts, userWithPolicy(t, cloud, user, doc)).GetSessionToken(ctx, &awssts.GetSessionTokenInput{})
		if err != nil {
			t.Fatalf("GetSessionToken %s: %v", user, err)
		}

		return sessionCreds(out.Credentials)
	}

	cases := []struct {
		name    string
		creds   aws.Credentials
		service string
		target  string
		denied  bool
	}{
		{"web identity session for a missing role", web, "dynamodb", listTables, true},
		{"role with no policies", assume("noperm"), "dynamodb", listTables, true},
		{"role allowed its action", assume("dynrole"), "dynamodb", listTables, false},
		{"role outside its policy", assume("dynrole"), "sqs", listQueues, true},
		{"session of a policy-limited user, outside policy", sessionFor("sqsonly", allowSQS), "dynamodb", listTables, true},
		{"session of a policy-limited user, inside policy", sessionFor("sqsonly2", allowSQS), "sqs", listQueues, false},
		{"session of an unrestricted user", sessionFor("free", ""), "dynamodb", listTables, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, typ := signedJSONRPC(t, ts, tc.creds, tc.service, tc.target)
			denied := status == http.StatusForbidden && typ == accessDeny

			if denied != tc.denied {
				t.Fatalf("status %d %s: denied = %v, want %v", status, typ, denied, tc.denied)
			}
		})
	}
}
