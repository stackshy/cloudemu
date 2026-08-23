package ec2_test

import (
	"context"
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
