package ec2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"
)

// leakedPrefixes are the internal cloudemu error-taxonomy names that must never
// appear as a prefix on a wire error message. Real AWS surfaces only the human
// sentence in <Message>.
var leakedPrefixes = []string{ //nolint:gochecknoglobals // shared test fixture
	"FailedPrecondition:",
	"NotFound:",
	"AlreadyExists:",
	"InvalidArgument:",
	"DependencyViolation:",
	"PermissionDenied:",
	"Internal:",
}

func apiMessage(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an API error, got %v", err)
	}

	return apiErr.ErrorMessage()
}

func assertNoLeakedPrefix(t *testing.T, msg string) {
	t.Helper()

	for _, p := range leakedPrefixes {
		if strings.HasPrefix(msg, p) || strings.Contains(msg, " "+p) {
			t.Errorf("wire message %q leaks internal error-code prefix %q", msg, p)
		}
	}
}

// TestDetachInternetGatewayNotAttached pins that detaching an internet gateway
// that was never attached to any VPC returns the AWS error code
// Gateway.NotAttached (not DependencyViolation, which is delete-while-attached).
func TestDetachInternetGatewayNotAttached(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	_, err = c.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
		InternetGatewayId: igw.InternetGateway.InternetGatewayId,
		VpcId:             vpc.Vpc.VpcId,
	})
	if err == nil {
		t.Fatal("DetachInternetGateway on an unattached gateway: want error, got nil")
	}

	if got := apiCode(t, err); got != "Gateway.NotAttached" {
		t.Errorf("error code = %q, want Gateway.NotAttached", got)
	}

	assertNoLeakedPrefix(t, apiMessage(t, err))
}

// TestDetachInternetGatewayWrongVpc pins that detaching an attached internet
// gateway while naming the wrong VPC also returns Gateway.NotAttached — the
// gateway is not attached to the VPC the caller specified.
func TestDetachInternetGatewayWrongVpc(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpcA, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc A: %v", err)
	}

	vpcB, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc B: %v", err)
	}

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: igw.InternetGateway.InternetGatewayId,
		VpcId:             vpcA.Vpc.VpcId,
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	_, err = c.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
		InternetGatewayId: igw.InternetGateway.InternetGatewayId,
		VpcId:             vpcB.Vpc.VpcId,
	})
	if err == nil {
		t.Fatal("DetachInternetGateway naming the wrong VPC: want error, got nil")
	}

	if got := apiCode(t, err); got != "Gateway.NotAttached" {
		t.Errorf("error code = %q, want Gateway.NotAttached", got)
	}
}

// TestDeleteInternetGatewayWhileAttachedIsDependencyViolation guards the sibling
// case: deleting (not detaching) an attached gateway is still DependencyViolation.
func TestDeleteInternetGatewayWhileAttachedIsDependencyViolation(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	vpc, err := c.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	igw, err := c.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}

	if _, err := c.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: igw.InternetGateway.InternetGatewayId,
		VpcId:             vpc.Vpc.VpcId,
	}); err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	_, err = c.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
		InternetGatewayId: igw.InternetGateway.InternetGatewayId,
	})
	if err == nil {
		t.Fatal("DeleteInternetGateway on an attached gateway: want error, got nil")
	}

	if got := apiCode(t, err); got != "DependencyViolation" {
		t.Errorf("error code = %q, want DependencyViolation", got)
	}
}

// TestEC2ErrorMessagesHaveNoInternalPrefix pins that EC2 wire error messages
// carry only the human sentence — no "FailedPrecondition:" / "DependencyViolation:"
// taxonomy prefix leaked from the internal error type.
func TestEC2ErrorMessagesHaveNoInternalPrefix(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	// (1) DeleteVpc with a subnet dependency -> DependencyViolation, clean message.
	vpcID, _ := mkVPCSubnet(t, c)

	_, err := c.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	if err == nil {
		t.Fatal("DeleteVpc with dependency: want error, got nil")
	}

	if got := apiCode(t, err); got != "DependencyViolation" {
		t.Errorf("DeleteVpc code = %q, want DependencyViolation", got)
	}

	assertNoLeakedPrefix(t, apiMessage(t, err))

	// (2) NotFound path -> clean message, no "NotFound:" prefix.
	_, err = c.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String("igw-doesnotexist"),
	})
	if err == nil {
		t.Fatal("DeleteInternetGateway on a missing gateway: want error, got nil")
	}

	assertNoLeakedPrefix(t, apiMessage(t, err))
}
