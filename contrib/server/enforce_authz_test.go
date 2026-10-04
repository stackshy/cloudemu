package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func credsConfig(t *testing.T, c aws.Credentials) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cfg
}

type awsClients struct {
	iam  *iam.Client
	ddb  *dynamodb.Client
	ec2  *ec2.Client
	as   *autoscaling.Client
	sqs  *sqs.Client
	cred aws.Credentials
}

func clientsFor(t *testing.T, endpoint string, c aws.Credentials) awsClients {
	t.Helper()

	cfg := credsConfig(t, c)
	ep := aws.String(endpoint)

	return awsClients{
		iam:  iam.NewFromConfig(cfg, func(o *iam.Options) { o.BaseEndpoint = ep }),
		ddb:  dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = ep }),
		ec2:  ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = ep }),
		as:   autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) { o.BaseEndpoint = ep }),
		sqs:  sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = ep }),
		cred: c,
	}
}

// newUser creates an IAM user with one inline policy and returns its key.
func (c awsClients) newUser(t *testing.T, name, doc string) aws.Credentials {
	t.Helper()

	ctx := context.Background()

	if _, err := c.iam.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
		t.Fatalf("CreateUser %s: %v", name, err)
	}

	if doc != "" {
		if _, err := c.iam.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
			UserName: aws.String(name), PolicyName: aws.String(name + "-p"), PolicyDocument: aws.String(doc),
		}); err != nil {
			t.Fatalf("PutUserPolicy %s: %v", name, err)
		}
	}

	out, err := c.iam.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateAccessKey %s: %v", name, err)
	}

	return aws.Credentials{AccessKeyID: aws.ToString(out.AccessKey.AccessKeyId), SecretAccessKey: aws.ToString(out.AccessKey.SecretAccessKey)}
}

func allowDoc(actions ...string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["` + strings.Join(actions, `","`) + `"],"Resource":"*"}]}`
}

// wantCode asserts err is the AWS API error code.
func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != code {
		t.Fatalf("%s: err = %v, want %s", what, err, code)
	}
}

func wantOK(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

const testAdminToken = "test-admin-token"

// adminDo calls a /_cloudemu endpoint, sending token as a bearer token when it
// is non-empty, and returns the status and body.
func adminDo(t *testing.T, method, endpoint, token string, body []byte) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

// adminCall calls a /_cloudemu endpoint with the admin token and requires 200.
func adminCall(t *testing.T, method, endpoint string, body []byte) []byte {
	t.Helper()

	status, raw := adminDo(t, method, endpoint, testAdminToken, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s with the admin token: %d %s", method, endpoint, status, raw)
	}

	return raw
}

// enforceAuthServer starts an --enforce-auth server with a known admin token.
func enforceAuthServer(t *testing.T) (string, func()) {
	t.Helper()

	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true
	cfg.EnforceAuth = true
	cfg.AdminToken = testAdminToken

	return startAWS(t, cfg, mustOptions(t, &cfg))
}

// seedBootUser creates the first IAM user through the admin seed endpoint, the
// documented bootstrap under --enforce-auth, and returns its key.
func seedBootUser(t *testing.T, endpoint string) aws.Credentials {
	t.Helper()

	creds := aws.Credentials{AccessKeyID: "AKIABOOTSTRAP0000001", SecretAccessKey: "boot-secret-key"}
	fixture := `{"iamUsers":[{"name":"boot","accessKeys":[{"accessKeyId":"` + creds.AccessKeyID +
		`","secretAccessKey":"` + creds.SecretAccessKey + `"}]}]}`
	adminCall(t, http.MethodPost, endpoint+"/_cloudemu/seed", []byte(fixture))

	return creds
}

// TestEnforceAuthAdminEndpointsNeedToken covers AUTHN-X3 end to end: under
// --enforce-auth the control plane refuses callers without the admin token,
// health stays open, and the token can bootstrap the first IAM user.
func TestEnforceAuthAdminEndpointsNeedToken(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	gated := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/_cloudemu/snapshot", nil},
		{http.MethodPost, "/_cloudemu/snapshot", []byte(`{"schemaVersion":1}`)},
		{http.MethodPost, "/_cloudemu/reset", nil},
		{http.MethodPost, "/_cloudemu/seed", []byte(`{"buckets":[{"name":"x"}]}`)},
		{http.MethodGet, "/_cloudemu/cost", nil},
	}

	for _, g := range gated {
		for _, token := range []string{"", "wrong-token"} {
			if status, _ := adminDo(t, g.method, endpoint+g.path, token, g.body); status != http.StatusUnauthorized {
				t.Errorf("%s %s token=%q = %d, want 401", g.method, g.path, token, status)
			}
		}
	}

	if status, _ := adminDo(t, http.MethodGet, endpoint+"/_cloudemu/health", "", nil); status != http.StatusOK {
		t.Fatalf("health without a token = %d, want 200", status)
	}

	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))

	ctx := context.Background()
	_, err := boot.iam.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("second")})
	wantOK(t, "CreateUser signed by the seeded key", err)

	snap := adminCall(t, http.MethodGet, endpoint+"/_cloudemu/snapshot", nil)
	if !strings.Contains(string(snap), "boot-secret-key") {
		t.Fatal("authenticated snapshot is missing the key secret needed for restore")
	}

	adminCall(t, http.MethodPost, endpoint+"/_cloudemu/reset", nil)

	_, err = boot.iam.ListUsers(ctx, &iam.ListUsersInput{})
	wantCode(t, "ListUsers after reset", err, "InvalidClientTokenId")

	adminCall(t, http.MethodPost, endpoint+"/_cloudemu/snapshot", snap)

	_, err = boot.iam.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("second")})
	wantOK(t, "GetUser after restore", err)
}

// TestEnforceAuthAuthorizesQueryAndREST drives real SDK clients against
// cloudemu serve with --enforce-auth: IAM, EC2, Auto Scaling and SQS calls are
// authorized against the caller's policies, while the admin endpoints take the
// admin token instead of a signature.
func TestEnforceAuthAuthorizesQueryAndREST(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	adminCall(t, http.MethodGet, endpoint+"/_cloudemu/health", nil)

	ctx := context.Background()
	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))

	t.Run("iam", func(t *testing.T) {
		limited := clientsFor(t, endpoint, boot.newUser(t, "limited", allowDoc("dynamodb:*")))

		_, err := limited.iam.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("evil")})
		wantCode(t, "CreateUser", err, "AccessDenied")
		_, err = limited.iam.ListUsers(ctx, &iam.ListUsersInput{})
		wantCode(t, "ListUsers", err, "AccessDenied")
		_, err = limited.iam.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String("limited")})
		wantCode(t, "CreateAccessKey", err, "AccessDenied")
		_, err = limited.ddb.ListTables(ctx, &dynamodb.ListTablesInput{})
		wantOK(t, "ListTables", err)

		_, err = boot.iam.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
			UserName: aws.String("limited"), PolicyName: aws.String("list"), PolicyDocument: aws.String(allowDoc("iam:ListUsers")),
		})
		wantOK(t, "PutUserPolicy", err)

		_, err = limited.iam.ListUsers(ctx, &iam.ListUsersInput{})
		wantOK(t, "ListUsers after the grant", err)
		_, err = limited.iam.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("evil")})
		wantCode(t, "CreateUser after the grant", err, "AccessDenied")

		if _, err := boot.iam.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("evil")}); err == nil {
			t.Fatal("a denied CreateUser created the user")
		}
	})

	t.Run("ec2", func(t *testing.T) {
		viewer := clientsFor(t, endpoint, boot.newUser(t, "ec2viewer", allowDoc("ec2:Describe*")))

		_, err := viewer.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
		wantOK(t, "DescribeInstances", err)
		_, err = viewer.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId: aws.String("ami-12345678"), InstanceType: ec2types.InstanceTypeT3Micro,
			MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		})
		wantCode(t, "RunInstances", err, "UnauthorizedOperation")
		_, err = viewer.as.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String("g"), MinSize: aws.Int32(0), MaxSize: aws.Int32(1),
			LaunchConfigurationName: aws.String("lc"), AvailabilityZones: []string{"us-east-1a"},
		})
		wantCode(t, "CreateAutoScalingGroup", err, "AccessDenied")
	})

	t.Run("sqs", func(t *testing.T) {
		q, err := boot.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("q1")})
		wantOK(t, "CreateQueue as boot", err)

		sender := clientsFor(t, endpoint, boot.newUser(t, "sender", allowDoc("sqs:SendMessage")))

		_, err = sender.sqs.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: q.QueueUrl, MessageBody: aws.String("hi")})
		wantOK(t, "SendMessage", err)
		_, err = sender.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("q2")})
		wantCode(t, "CreateQueue", err, "AccessDeniedException")

		// A query-form SendMessage is served by EC2's catch-all, never by SQS,
		// and is authorized as ec2:SendMessage.
		status, body := signedForm(t, endpoint, sender.cred, "sqs", url.Values{
			"Action": {"SendMessage"}, "QueueUrl": {aws.ToString(q.QueueUrl)}, "MessageBody": {"forged"},
		})
		if status != http.StatusForbidden || !strings.Contains(body, "ec2:SendMessage") {
			t.Fatalf("query-form SendMessage: %d %s", status, body)
		}

		attrs, err := boot.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages},
		})
		wantOK(t, "GetQueueAttributes", err)

		if n := attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)]; n != "1" {
			t.Fatalf("queue holds %s messages, want 1", n)
		}
	})

	t.Run("admin reset with the token", func(t *testing.T) {
		adminCall(t, http.MethodPost, endpoint+"/_cloudemu/reset", nil)
	})
}

// signedForm sends a SigV4-signed query-protocol POST and returns the status
// and body.
func signedForm(t *testing.T, endpoint string, c aws.Credentials, service string, form url.Values) (int, string) {
	t.Helper()

	body := form.Encode()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(context.Background(), c, req, hex.EncodeToString(sum[:]), service, "us-east-1", time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}
