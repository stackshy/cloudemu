package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
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

// TestCompatAWSIAMAuthorizationQuery covers the query protocol (EC2): an IAM
// user allowed only ec2:DescribeInstances can describe but not run instances.
func TestCompatAWSIAMAuthorizationQuery(t *testing.T) {
	cloud := cloudemu.NewAWS()
	akid, secret := registerUserWithKey(t, cloud.IAM, "ec2user")
	attachInlinePolicy(t, cloud.IAM, "ec2user", "ec2-describe-only", `{
		"Version": "2012-10-17",
		"Statement": [{"Effect": "Allow", "Action": "ec2:DescribeInstances", "Resource": "*"}]
	}`)

	sess := compat.BootAWS(t, awsserver.Drivers{
		IAM:         cloud.IAM,
		EC2:         cloud.EC2,
		EnforceAuth: true,
	})
	client := ec2.NewFromConfig(staticConfig(t, akid, secret), func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	if _, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatalf("DescribeInstances should be authorized, got: %v", err)
	}

	_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-12345678"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if code := apiErrorCode(t, err); code != "UnauthorizedOperation" {
		t.Fatalf("RunInstances: want UnauthorizedOperation, got %q", code)
	}
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
	if _, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
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

	if _, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
	}); err != nil {
		t.Fatalf("GetItem should be authorized, got: %v", err)
	}

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
	})
	if code := apiErrorCode(t, err); code != "AccessDeniedException" {
		t.Fatalf("PutItem: want AccessDeniedException, got %q", code)
	}
}

// TestCompatAWSIAMAuthorizationGroup proves group-inherited permissions are
// honored end-to-end: a user with no attached policy but membership in a group
// that allows the action can perform it.
func TestCompatAWSIAMAuthorizationGroup(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	akid, secret := registerUserWithKey(t, cloud.IAM, "groupuser")
	if _, err := cloud.IAM.CreateGroup(ctx, iamdriver.GroupConfig{Name: "ec2-readers"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "ec2-describe",
		PolicyDocument: `{"Version":"2012-10-17","Statement":[` +
			`{"Effect":"Allow","Action":"ec2:DescribeInstances","Resource":"*"}]}`,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if err := cloud.IAM.AttachGroupPolicy(ctx, "ec2-readers", pol.ARN); err != nil {
		t.Fatalf("AttachGroupPolicy: %v", err)
	}
	if err := cloud.IAM.AddUserToGroup(ctx, "groupuser", "ec2-readers"); err != nil {
		t.Fatalf("AddUserToGroup: %v", err)
	}

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, EC2: cloud.EC2, EnforceAuth: true})
	client := ec2.NewFromConfig(staticConfig(t, akid, secret), func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	if _, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatalf("DescribeInstances via group policy should be authorized, got: %v", err)
	}
}

// TestCompatAWSIAMAuthorizationBoundary proves a permissions boundary caps an
// otherwise-admin user: an attached allow-all policy still cannot run an action
// the boundary does not permit.
func TestCompatAWSIAMAuthorizationBoundary(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	akid, secret := registerUserWithKey(t, cloud.IAM, "boundeduser")
	attachInlinePolicy(t, cloud.IAM, "boundeduser", "admin-all",
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)

	boundary, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "s3-only-boundary",
		PolicyDocument: `{"Version":"2012-10-17","Statement":[` +
			`{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if err := cloud.IAM.PutUserPermissionsBoundary(ctx, "boundeduser", boundary.ARN); err != nil {
		t.Fatalf("PutUserPermissionsBoundary: %v", err)
	}

	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM, EC2: cloud.EC2, EnforceAuth: true})
	client := ec2.NewFromConfig(staticConfig(t, akid, secret), func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	_, err = client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-12345678"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	})
	if code := apiErrorCode(t, err); code != "UnauthorizedOperation" {
		t.Fatalf("RunInstances outside boundary: want UnauthorizedOperation, got %q", code)
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
