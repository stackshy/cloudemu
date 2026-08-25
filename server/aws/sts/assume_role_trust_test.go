package sts_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// loadTestConfig builds an aws.Config pointed at the test region with static
// dummy credentials (cloudemu does not verify signatures).
func loadTestConfig() (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
}

const allowRootTrust = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
	`"Principal":{"AWS":"arn:aws:iam::123456789012:root"},"Action":"sts:AssumeRole"}]}`

const denyTrust = `{"Version":"2012-10-17","Statement":[{"Effect":"Deny",` +
	`"Principal":"*","Action":"sts:AssumeRole"}]}`

// newTrustServer wires IAM + STS so AssumeRole enforces role trust policies.
func newTrustServer(t *testing.T) (*httptest.Server, *awsiam.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{
		IAM:       cloud.IAM,
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	}))
	t.Cleanup(ts.Close)

	cfg, err := loadTestConfig()
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	iamClient := awsiam.NewFromConfig(cfg, func(o *awsiam.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return ts, iamClient
}

func TestSDKAssumeRoleTrustAllows(t *testing.T) {
	ts, iamClient := newTrustServer(t)
	ctx := context.Background()

	if _, err := iamClient.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName:                 aws.String("allowrole"),
		AssumeRolePolicyDocument: aws.String(allowRootTrust),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	out, err := stsClient(t, ts.URL).AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/allowrole"),
		RoleSessionName: aws.String("s"),
	})
	if err != nil {
		t.Fatalf("AssumeRole (allow): %v", err)
	}

	if out.Credentials == nil || aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Fatalf("AssumeRole (allow) returned no credentials")
	}
}

func TestSDKAssumeRoleTrustDenies(t *testing.T) {
	ts, iamClient := newTrustServer(t)
	ctx := context.Background()

	if _, err := iamClient.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName:                 aws.String("denyrole"),
		AssumeRolePolicyDocument: aws.String(denyTrust),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	_, err := stsClient(t, ts.URL).AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/denyrole"),
		RoleSessionName: aws.String("s"),
	})
	assertAccessDenied(t, err)
}

func TestSDKAssumeRoleNonexistentRoleDenied(t *testing.T) {
	ts, _ := newTrustServer(t)

	_, err := stsClient(t, ts.URL).AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/ghost"),
		RoleSessionName: aws.String("s"),
	})
	assertAccessDenied(t, err)
}

// TestSDKAssumeRolePermissiveWithoutIAM confirms the standalone stance: with no
// IAM driver wired, AssumeRole still returns creds (init-creds behavior).
func TestSDKAssumeRolePermissiveWithoutIAM(t *testing.T) {
	ts := newServer(t, awsserver.Drivers{
		STS:       true,
		AccountID: testAccountID,
		Region:    testRegion,
	})

	out, err := stsClient(t, ts.URL).AssumeRole(context.Background(), &awssts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + testAccountID + ":role/anything"),
		RoleSessionName: aws.String("s"),
	})
	if err != nil {
		t.Fatalf("AssumeRole (no IAM): %v", err)
	}

	if out.Credentials == nil {
		t.Fatalf("AssumeRole (no IAM) returned no credentials")
	}
}

func assertAccessDenied(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("AssumeRole: want AccessDenied, got nil (silent success)")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("AssumeRole: want smithy.APIError, got %T: %v", err, err)
	}

	if apiErr.ErrorCode() != "AccessDenied" {
		t.Fatalf("AssumeRole: want code AccessDenied, got %q", apiErr.ErrorCode())
	}

	var respErr *awshttp.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("AssumeRole: want *awshttp.ResponseError, got %T", err)
	}

	if respErr.HTTPStatusCode() != 403 {
		t.Fatalf("AssumeRole: want HTTP 403, got %d", respErr.HTTPStatusCode())
	}
}
