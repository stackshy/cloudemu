package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// staticConfig builds an aws-sdk-go-v2 config that signs with the given static
// credentials, so a real SDK client authenticates as a specific IAM user.
func staticConfig(t *testing.T, accessKeyID, secret string) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secret, ""),
		),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	return cfg
}

// attachInlinePolicy grants a user an inline policy document directly on the
// driver, used to set up an authenticated caller's permissions for the test.
func attachInlinePolicy(t *testing.T, m iamdriver.IAM, userName, name, doc string) {
	t.Helper()

	putter, ok := m.(interface {
		PutUserPolicy(ctx context.Context, userName, policyName, policyDocument string) error
	})
	if !ok {
		t.Fatal("IAM driver does not support PutUserPolicy")
	}

	if err := putter.PutUserPolicy(context.Background(), userName, name, doc); err != nil {
		t.Fatalf("PutUserPolicy: %v", err)
	}
}

// createTable creates a simple hash-keyed table via a real DynamoDB client.
func createTable(t *testing.T, client *dynamodb.Client, table string) {
	t.Helper()

	if _, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
	}); err != nil {
		t.Fatalf("CreateTable should be authorized, got: %v", err)
	}
}

func ddbKey(id string) map[string]ddbtypes.AttributeValue {
	return map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: id}}
}

// TestCompatAWSIAMAuthorizationJSONRPC covers the JSON-RPC protocol (DynamoDB):
// a user allowed GetItem/CreateTable but not PutItem is denied PutItem with
// AccessDeniedException.
func TestCompatAWSIAMAuthorizationJSONRPC(t *testing.T) {
	cloud := cloudemu.NewAWS()
	akid, secret := registerUserWithKey(t, cloud.IAM, "ddbuser")
	attachInlinePolicy(t, cloud.IAM, "ddbuser", "ddb-read-create", `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Action": ["dynamodb:CreateTable", "dynamodb:DescribeTable", "dynamodb:GetItem"],
			"Resource": "*"
		}]
	}`)

	sess := compat.BootAWS(t, awsserver.Drivers{
		IAM:         cloud.IAM,
		DynamoDB:    cloud.DynamoDB,
		EnforceAuth: true,
	})
	client := dynamodb.NewFromConfig(staticConfig(t, akid, secret), func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const table = "authz-items"
	createTable(t, client, table)

	if _, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key:       ddbKey("a"),
	}); err != nil {
		t.Fatalf("GetItem should be authorized, got: %v", err)
	}

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      ddbKey("a"),
	})
	if code := apiErrorCode(t, err); code != "AccessDeniedException" {
		t.Fatalf("PutItem: want AccessDeniedException, got %q", code)
	}
}

// TestCompatAWSIAMAuthorizationGroup proves group-inherited permissions are
// honored end-to-end: a user with no attached policy but membership in a group
// that allows the action can perform it (JSON-RPC / DynamoDB).
func TestCompatAWSIAMAuthorizationGroup(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	akid, secret := registerUserWithKey(t, cloud.IAM, "groupuser")
	if _, err := cloud.IAM.CreateGroup(ctx, iamdriver.GroupConfig{Name: "ddb-readers"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "ddb-read-create",
		PolicyDocument: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Action":["dynamodb:CreateTable","dynamodb:DescribeTable","dynamodb:GetItem"],"Resource":"*"}]}`,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if err := cloud.IAM.AttachGroupPolicy(ctx, "ddb-readers", pol.ARN); err != nil {
		t.Fatalf("AttachGroupPolicy: %v", err)
	}
	if err := cloud.IAM.AddUserToGroup(ctx, "groupuser", "ddb-readers"); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, DynamoDB: cloud.DynamoDB, EnforceAuth: true})
	client := dynamodb.NewFromConfig(staticConfig(t, akid, secret), func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	const table = "group-items"
	createTable(t, client, table)

	if _, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), Key: ddbKey("a")}); err != nil {
		t.Fatalf("GetItem via group policy should be authorized, got: %v", err)
	}
}

// TestCompatAWSIAMAuthorizationBoundary proves a permissions boundary caps an
// otherwise-admin user: an attached allow-all policy still cannot run an action
// the boundary does not permit (JSON-RPC / DynamoDB).
func TestCompatAWSIAMAuthorizationBoundary(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	akid, secret := registerUserWithKey(t, cloud.IAM, "boundeduser")
	attachInlinePolicy(t, cloud.IAM, "boundeduser", "admin-all",
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)

	boundary, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "ddb-read-boundary",
		PolicyDocument: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Action":["dynamodb:CreateTable","dynamodb:DescribeTable","dynamodb:GetItem"],"Resource":"*"}]}`,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if err := cloud.IAM.PutUserPermissionsBoundary(ctx, "boundeduser", boundary.ARN); err != nil {
		t.Fatalf("PutUserPermissionsBoundary: %v", err)
	}

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, DynamoDB: cloud.DynamoDB, EnforceAuth: true})
	client := dynamodb.NewFromConfig(staticConfig(t, akid, secret), func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	const table = "bounded-items"
	createTable(t, client, table)

	// Within the boundary: GetItem is allowed by both the admin policy and the boundary.
	if _, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), Key: ddbKey("a")}); err != nil {
		t.Fatalf("GetItem within boundary should be authorized, got: %v", err)
	}

	// Outside the boundary: PutItem is allowed by the admin policy but not the boundary.
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(table), Item: ddbKey("a")})
	if code := apiErrorCode(t, err); code != "AccessDeniedException" {
		t.Fatalf("PutItem outside boundary: want AccessDeniedException, got %q", code)
	}
}

// TestCompatAWSAuthorizationCrossServiceBypassClosed is the regression guard for
// the credential-scope authorization bypass. It signs a request with a SigV4
// credential scope of "s3" (which the caller's policy fully allows) but sends a
// DynamoDB X-Amz-Target. Because the gate derives the IAM service from the
// dispatch key (X-Amz-Target -> dynamodb), not the client-controlled scope, the
// action authorized is dynamodb:PutItem — which the caller is NOT granted — so
// the request is denied even though the signed scope is s3.
func TestCompatAWSAuthorizationCrossServiceBypassClosed(t *testing.T) {
	cloud := cloudemu.NewAWS()
	akid, secret := registerUserWithKey(t, cloud.IAM, "bypasser")
	// Full S3 access, nothing else. If the service came from the scope this would
	// resolve to s3:PutItem (allowed) while the DynamoDB PutItem handler runs.
	attachInlinePolicy(t, cloud.IAM, "bypasser", "s3-full",
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, DynamoDB: cloud.DynamoDB, EnforceAuth: true})

	body := []byte(`{"TableName":"anything","Item":{"id":{"S":"a"}}}`)
	req, err := http.NewRequest(http.MethodPost, sess.Endpoint()+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")

	// Sign with service "s3" so authentication (which reads the scope service)
	// succeeds, while the X-Amz-Target still names DynamoDB.
	sum := sha256.Sum256(body)
	creds := aws.Credentials{AccessKeyID: akid, SecretAccessKey: secret}
	if err := v4.NewSigner().SignHTTP(
		context.Background(), creds, req, hex.EncodeToString(sum[:]), "s3", "us-east-1", time.Now().UTC(),
	); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-service bypass not closed: want 403, got %d (%s)", resp.StatusCode, raw)
	}

	var errBody struct {
		Type string `json:"__type"`
	}
	if err := json.Unmarshal(raw, &errBody); err != nil {
		t.Fatalf("decode error body %q: %v", raw, err)
	}
	if errBody.Type != "AccessDeniedException" {
		t.Fatalf("want __type AccessDeniedException, got %q (%s)", errBody.Type, raw)
	}
}

// TestCompatAWSAuthorizationQueryAuthenticatedOnly pins the documented limitation
// that the query protocol is authenticated but NOT authorization-enforced in this
// revision: a user whose policy grants only dynamodb:GetItem (no EC2 permission)
// can still make an authenticated EC2 query call, because query authorization —
// which cannot be soundly bound to the executed operation before dispatch — is a
// follow-up.
func TestCompatAWSAuthorizationQueryAuthenticatedOnly(t *testing.T) {
	cloud := cloudemu.NewAWS()
	akid, secret := registerUserWithKey(t, cloud.IAM, "queryuser")
	attachInlinePolicy(t, cloud.IAM, "queryuser", "ddb-get-only",
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:GetItem","Resource":"*"}]}`)

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, EC2: cloud.EC2, EnforceAuth: true})
	client := ec2.NewFromConfig(staticConfig(t, akid, secret), func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	if _, err := client.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatalf("query call is authenticated-only and should succeed, got: %v", err)
	}
}

// TestCompatAWSAuthorizationDisabled confirms that with EnforceAuth off (the
// default) neither authentication nor authorization applies: the harness's dummy
// creds run any action, so bootstrap flows are unaffected.
func TestCompatAWSAuthorizationDisabled(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, EC2: cloud.EC2, DynamoDB: cloud.DynamoDB})
	ctx := context.Background()

	ec2c := ec2.NewFromConfig(sess.Config(), func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	if _, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatalf("DescribeInstances with auth disabled should succeed, got: %v", err)
	}

	ddbc := dynamodb.NewFromConfig(sess.Config(), func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	if _, err := ddbc.ListTables(ctx, &dynamodb.ListTablesInput{}); err != nil {
		t.Fatalf("ListTables with auth disabled should succeed, got: %v", err)
	}
}
