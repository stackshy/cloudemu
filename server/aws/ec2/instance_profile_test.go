package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEC2AndIAMClients wires one in-process AWS server and returns real
// aws-sdk-go-v2 EC2 and IAM clients pointed at it, so the role->profile->instance
// reference chain can be driven end to end.
func newEC2AndIAMClients(t *testing.T) (*ec2.Client, *iam.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return ec2.NewFromConfig(cfg), iam.NewFromConfig(cfg)
}

// TestRunInstancesIamInstanceProfileReflectedOnDescribe drives the real-user
// flow: create an IAM role, wrap it in an instance profile, launch an instance
// referencing that profile by name, and confirm the launched instance and a
// follow-up DescribeInstances both echo the profile's ARN (the role->profile->
// instance reference resolves, matching the EC2 iamInstanceProfile field).
func TestRunInstancesIamInstanceProfileReflectedOnDescribe(t *testing.T) {
	ctx := context.Background()
	ec2c, iamc := newEC2AndIAMClients(t)

	const profileName = "web-server-profile"

	if _, err := iamc.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("web-server-role"),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	profile, err := iamc.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		t.Fatalf("CreateInstanceProfile: %v", err)
	}

	wantARN := aws.ToString(profile.InstanceProfile.Arn)
	if wantARN == "" {
		t.Fatal("CreateInstanceProfile returned an empty ARN")
	}

	if _, err := iamc.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String("web-server-role"),
	}); err != nil {
		t.Fatalf("AddRoleToInstanceProfile: %v", err)
	}

	run, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: aws.String(profileName),
		},
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	launched := run.Instances[0]
	if launched.IamInstanceProfile == nil {
		t.Fatal("RunInstances dropped IamInstanceProfile: instance has none")
	}

	if got := aws.ToString(launched.IamInstanceProfile.Arn); got != wantARN {
		t.Fatalf("RunInstances IamInstanceProfile.Arn = %q, want %q", got, wantARN)
	}

	if aws.ToString(launched.IamInstanceProfile.Id) == "" {
		t.Fatal("RunInstances IamInstanceProfile.Id is empty")
	}

	id := aws.ToString(launched.InstanceId)

	desc, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}

	got := desc.Reservations[0].Instances[0]
	if got.IamInstanceProfile == nil {
		t.Fatal("DescribeInstances dropped IamInstanceProfile: instance has none")
	}

	if arn := aws.ToString(got.IamInstanceProfile.Arn); arn != wantARN {
		t.Fatalf("DescribeInstances IamInstanceProfile.Arn = %q, want %q", arn, wantARN)
	}

	if aws.ToString(got.IamInstanceProfile.Id) != aws.ToString(profile.InstanceProfile.InstanceProfileId) {
		t.Fatalf("DescribeInstances IamInstanceProfile.Id = %q, want %q",
			aws.ToString(got.IamInstanceProfile.Id),
			aws.ToString(profile.InstanceProfile.InstanceProfileId))
	}
}
