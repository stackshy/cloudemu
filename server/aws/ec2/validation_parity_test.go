package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// assertAPIErr asserts the error is an API error with the given code and a 400
// HTTP status, matching real EC2's validation-failure responses.
func assertAPIErr(t *testing.T, err error, wantCode string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %q, got nil", wantCode)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want smithy.APIError", err, err)
	}

	if apiErr.ErrorCode() != wantCode {
		t.Fatalf("error code = %q, want %q", apiErr.ErrorCode(), wantCode)
	}

	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("error = %T %v, want *awshttp.ResponseError", err, err)
	}

	if re.HTTPStatusCode() != 400 {
		t.Fatalf("HTTP status = %d, want 400", re.HTTPStatusCode())
	}
}

// TestCreateVpcCIDRValidation pins the CreateVpc CIDR checks real EC2 performs:
// a malformed block is InvalidParameterValue, and a valid block whose netmask is
// outside /16../28 is InvalidVpcRange. Both are HTTP 400 and create nothing.
func TestCreateVpcCIDRValidation(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	malformed := []string{"not-a-cidr", "300.0.0.0/16", "10.0.0.0/33"}
	for _, cidr := range malformed {
		_, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
		assertAPIErr(t, err, "InvalidParameterValue")
	}

	outOfRange := []string{"10.0.0.0/8", "10.0.0.0/29"}
	for _, cidr := range outOfRange {
		_, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(cidr)})
		assertAPIErr(t, err, "InvalidVpcRange")
	}

	// A well-formed in-range block still succeeds — no happy-path regression.
	ok, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc(10.0.0.0/16): %v", err)
	}
	if aws.ToString(ok.Vpc.VpcId) == "" {
		t.Fatalf("CreateVpc(10.0.0.0/16) returned no VpcId")
	}
}

// TestRunInstancesCountValidation pins that MinCount must be >= 1 and MaxCount
// >= MinCount; a violating request is InvalidParameterValue (HTTP 400) and
// launches nothing.
func TestRunInstancesCountValidation(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	// MinCount greater than MaxCount.
	_, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-12345678"),
		MinCount: aws.Int32(5),
		MaxCount: aws.Int32(2),
	})
	assertAPIErr(t, err, "InvalidParameterValue")

	// MinCount / MaxCount of zero.
	_, err = client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-12345678"),
		MinCount: aws.Int32(0),
		MaxCount: aws.Int32(0),
	})
	assertAPIErr(t, err, "InvalidParameterValue")

	// A valid MinCount=1/MaxCount=3 still launches MaxCount instances.
	out, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:  aws.String("ami-12345678"),
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(3),
	})
	if err != nil {
		t.Fatalf("RunInstances(1,3): %v", err)
	}
	if len(out.Instances) != 3 {
		t.Fatalf("launched %d instances, want 3", len(out.Instances))
	}
}

// TestCreateTagsRestrictions pins the CreateTags limits real EC2 enforces:
// at most 50 user tags per resource (TagLimitExceeded) and no key or value in
// the reserved "aws:" namespace (InvalidParameterValue). Both are HTTP 400.
func TestCreateTagsRestrictions(t *testing.T) {
	ctx := context.Background()
	client := newEC2(t)

	vpc, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	id := aws.ToString(vpc.Vpc.VpcId)

	// 51 distinct tags exceed the 50-tag ceiling.
	tooMany := make([]ec2types.Tag, 0, 51)
	for i := 0; i < 51; i++ {
		tooMany = append(tooMany, ec2types.Tag{
			Key:   aws.String("k" + string(rune('a'+i%26)) + string(rune('a'+i/26))),
			Value: aws.String("v"),
		})
	}
	_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{id}, Tags: tooMany})
	assertAPIErr(t, err, "TagLimitExceeded")

	// A key in the reserved aws: namespace is rejected.
	_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags:      []ec2types.Tag{{Key: aws.String("aws:foo"), Value: aws.String("bar")}},
	})
	assertAPIErr(t, err, "InvalidParameterValue")

	// A value in the reserved aws: namespace is rejected.
	_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags:      []ec2types.Tag{{Key: aws.String("foo"), Value: aws.String("aws:bar")}},
	})
	assertAPIErr(t, err, "InvalidParameterValue")

	// A normal small tag set still applies cleanly.
	_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{id},
		Tags:      []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("web")}},
	})
	if err != nil {
		t.Fatalf("CreateTags(normal): %v", err)
	}
}
