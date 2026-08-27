package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestEC2AccountIDMatchesSTS pins that EC2 owner-ids use the SAME configured
// account id as the rest of the AWS server. EC2 previously hardcoded
// 123456789012 while STS/IAM/SQS/... reported the configured id, so anything
// parsing owner-ids/ARNs across services (cross-service IaC, cost tooling) saw
// two different accounts. A custom --account-id must now be reflected by EC2's
// VPC ownerId AND by STS GetCallerIdentity, and the two must agree.
func TestEC2AccountIDMatchesSTS(t *testing.T) {
	const wantAccount = "999988887777"

	ctx := context.Background()

	cloud := cloudemu.NewAWS(config.WithAccountID(wantAccount))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	ec2Client := ec2.NewFromConfig(cfg)
	stsClient := sts.NewFromConfig(cfg)

	vpc, err := ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	ec2Owner := aws.ToString(vpc.Vpc.OwnerId)
	if ec2Owner != wantAccount {
		t.Errorf("EC2 VPC ownerId = %q, want configured account %q", ec2Owner, wantAccount)
	}

	ident, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity: %v", err)
	}

	stsAccount := aws.ToString(ident.Account)
	if stsAccount != wantAccount {
		t.Errorf("STS account = %q, want configured account %q", stsAccount, wantAccount)
	}

	// The whole point: EC2 and its sibling services agree on one account id.
	if ec2Owner != stsAccount {
		t.Errorf("cross-service account mismatch: EC2 ownerId %q != STS account %q", ec2Owner, stsAccount)
	}
}
