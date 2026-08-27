package ec2_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEC2Client wires a full in-process AWS server and returns a real
// aws-sdk-go-v2 EC2 client pointed at it.
func newEC2Client(t *testing.T) *ec2.Client {
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

	return ec2.NewFromConfig(cfg)
}

func runOneInstance(t *testing.T, c *ec2.Client) string {
	t.Helper()

	run, err := c.RunInstances(context.Background(), &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-123"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	return aws.ToString(run.Instances[0].InstanceId)
}

// TestModifyDisableApiTerminationTakesEffect pins that
// ModifyInstanceAttribute(DisableApiTermination=true) is honored: it is
// verifiable via DescribeInstanceAttribute and blocks TerminateInstances with
// OperationNotPermitted (previously accepted-and-discarded — a false success
// dangerous for IaC termination protection).
func TestModifyDisableApiTerminationTakesEffect(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId:            aws.String(id),
		DisableApiTermination: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute: %v", err)
	}

	attr, err := c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameDisableApiTermination,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute: %v", err)
	}

	if attr.DisableApiTermination == nil || !aws.ToBool(attr.DisableApiTermination.Value) {
		t.Fatalf("disableApiTermination not persisted: %+v", attr.DisableApiTermination)
	}

	_, err = c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}})
	if err == nil {
		t.Fatal("TerminateInstances succeeded despite termination protection")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "OperationNotPermitted" {
		t.Fatalf("want OperationNotPermitted, got %v", err)
	}

	// Clearing protection lets the terminate through.
	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId:            aws.String(id),
		DisableApiTermination: &ec2types.AttributeBooleanValue{Value: aws.Bool(false)},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute clear: %v", err)
	}

	if _, err := c.TerminateInstances(ctx,
		&ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		t.Fatalf("TerminateInstances after clearing protection: %v", err)
	}
}

// TestModifySourceDestCheckRoundTrips pins that SourceDestCheck defaults to true
// and a ModifyInstanceAttribute(SourceDestCheck=false) is honored and readable
// back via DescribeInstanceAttribute.
func TestModifySourceDestCheckRoundTrips(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	got, err := c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameSourceDestCheck,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute: %v", err)
	}

	if got.SourceDestCheck == nil || !aws.ToBool(got.SourceDestCheck.Value) {
		t.Fatalf("sourceDestCheck default should be true, got %+v", got.SourceDestCheck)
	}

	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId:      aws.String(id),
		SourceDestCheck: &ec2types.AttributeBooleanValue{Value: aws.Bool(false)},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute: %v", err)
	}

	got, err = c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameSourceDestCheck,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute: %v", err)
	}

	if got.SourceDestCheck == nil || aws.ToBool(got.SourceDestCheck.Value) {
		t.Fatalf("sourceDestCheck should be false after modify, got %+v", got.SourceDestCheck)
	}
}

// TestModifyUserDataRoundTrips pins that ModifyInstanceAttribute(UserData) is
// honored and read back as the base64 blob via DescribeInstanceAttribute
// (previously accepted and silently discarded).
func TestModifyUserDataRoundTrips(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	// UserData changes require a stopped instance on real EC2.
	if _, err := c.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	userData := []byte("#!/bin/bash\necho hello")
	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(id),
		UserData:   &ec2types.BlobAttributeValue{Value: userData},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute(UserData): %v", err)
	}

	got, err := c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameUserData,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(UserData): %v", err)
	}

	want := base64.StdEncoding.EncodeToString(userData)
	if got.UserData == nil || aws.ToString(got.UserData.Value) != want {
		t.Fatalf("userData not persisted: got %+v want %q", got.UserData, want)
	}
}

// TestModifyEbsOptimizedRoundTrips pins that
// ModifyInstanceAttribute(EbsOptimized=true) is honored and read back via
// DescribeInstanceAttribute.
func TestModifyEbsOptimizedRoundTrips(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId:   aws.String(id),
		EbsOptimized: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute(EbsOptimized): %v", err)
	}

	got, err := c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameEbsOptimized,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(EbsOptimized): %v", err)
	}

	if got.EbsOptimized == nil || !aws.ToBool(got.EbsOptimized.Value) {
		t.Fatalf("ebsOptimized not persisted: %+v", got.EbsOptimized)
	}
}

// TestModifyInstanceGroupsRoundTrips pins that ModifyInstanceAttribute(Groups)
// replaces the instance's security-group membership and is read back via
// DescribeInstanceAttribute(groupSet) with the resolved group name.
func TestModifyInstanceGroupsRoundTrips(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)
	id := runOneInstance(t, c)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("modify-groups-sg"),
		Description: aws.String("test"),
		VpcId:       vpc.Vpc.VpcId,
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	sgID := aws.ToString(sg.GroupId)
	if _, err := c.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(id),
		Groups:     []string{sgID},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute(Groups): %v", err)
	}

	got, err := c.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameGroupSet,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(groupSet): %v", err)
	}

	if len(got.Groups) != 1 || aws.ToString(got.Groups[0].GroupId) != sgID {
		t.Fatalf("group membership not applied: %+v", got.Groups)
	}

	if aws.ToString(got.Groups[0].GroupName) != "modify-groups-sg" {
		t.Fatalf("group name not resolved: %+v", got.Groups[0])
	}
}
