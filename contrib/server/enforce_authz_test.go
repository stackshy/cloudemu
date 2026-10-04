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

func adminCall(t *testing.T, method, endpoint string, body []byte) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unsigned %s %s under --enforce-auth: %d %s", method, endpoint, resp.StatusCode, raw)
	}

	return raw
}

// bootstrapUser starts a server with auth off, creates a policy-less "boot"
// user with a key, and returns the whole-emulator snapshot holding it.
func bootstrapUser(t *testing.T) ([]byte, aws.Credentials) {
	t.Helper()

	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true

	url, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	boot := clientsFor(t, url, aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}).newUser(t, "boot", "")

	return adminCall(t, http.MethodGet, url+"/_cloudemu/snapshot", nil), boot
}

// TestEnforceAuthAuthorizesQueryAndREST drives real SDK clients against
// cloudemu serve with --enforce-auth: IAM, EC2, Auto Scaling and SQS calls are
// authorized against the caller's policies, while the admin endpoints stay
// unsigned.
func TestEnforceAuthAuthorizesQueryAndREST(t *testing.T) {
	snapshot, bootCreds := bootstrapUser(t)

	cfg := testConfig(t, allEnginesOff())
	cfg.Admin = true
	cfg.EnforceAuth = true

	endpoint, stop := startAWS(t, cfg, mustOptions(t, &cfg))
	defer stop()

	adminCall(t, http.MethodGet, endpoint+"/_cloudemu/health", nil)
	adminCall(t, http.MethodPost, endpoint+"/_cloudemu/snapshot", snapshot)

	ctx := context.Background()
	boot := clientsFor(t, endpoint, bootCreds)

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

	t.Run("admin reset stays unsigned", func(t *testing.T) {
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
