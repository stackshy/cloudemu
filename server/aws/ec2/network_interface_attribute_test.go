package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestCreateNetworkInterfaceDefaults pins the attributes real EC2 auto-assigns to
// a standalone ENI: a private IPv4 from the subnet CIDR, a MAC address, and
// SourceDestCheck defaulting to true. Terraform aws_network_interface reads all
// three straight off the create/describe response.
func TestCreateNetworkInterfaceDefaults(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	created, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetID),
		Description: aws.String("app-eni"),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	eni := created.NetworkInterface
	if got := aws.ToString(eni.PrivateIpAddress); got == "" {
		t.Error("create: privateIpAddress is empty, want a subnet-scoped address")
	}

	if got := aws.ToString(eni.MacAddress); got == "" {
		t.Error("create: macAddress is empty")
	}

	if eni.SourceDestCheck == nil || !aws.ToBool(eni.SourceDestCheck) {
		t.Errorf("create: sourceDestCheck = %v, want true", eni.SourceDestCheck)
	}

	// The same attributes must survive a Describe round-trip.
	id := aws.ToString(eni.NetworkInterfaceId)

	desc, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	got := desc.NetworkInterfaces[0]
	if aws.ToString(got.PrivateIpAddress) != aws.ToString(eni.PrivateIpAddress) {
		t.Errorf("describe privateIpAddress = %q, want %q",
			aws.ToString(got.PrivateIpAddress), aws.ToString(eni.PrivateIpAddress))
	}

	if got.SourceDestCheck == nil || !aws.ToBool(got.SourceDestCheck) {
		t.Errorf("describe sourceDestCheck = %v, want true", got.SourceDestCheck)
	}
}

// TestModifyNetworkInterfaceSourceDestCheck pins that
// ModifyNetworkInterfaceAttribute(SourceDestCheck=false) is honored and readable
// back — the required step for a NAT-instance / firewall / router VM.
func TestModifyNetworkInterfaceSourceDestCheck(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	created, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	id := aws.ToString(created.NetworkInterface.NetworkInterfaceId)

	if _, err := c.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(id),
		SourceDestCheck:    &ec2types.AttributeBooleanValue{Value: aws.Bool(false)},
	}); err != nil {
		t.Fatalf("ModifyNetworkInterfaceAttribute: %v", err)
	}

	desc, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	if got := desc.NetworkInterfaces[0].SourceDestCheck; got == nil || aws.ToBool(got) {
		t.Errorf("sourceDestCheck after modify = %v, want false", got)
	}
}

// TestModifyNetworkInterfaceDescription pins that a Description update round-trips.
func TestModifyNetworkInterfaceDescription(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	created, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetID),
		Description: aws.String("before"),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	id := aws.ToString(created.NetworkInterface.NetworkInterfaceId)

	if _, err := c.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(id),
		Description:        &ec2types.AttributeValue{Value: aws.String("after")},
	}); err != nil {
		t.Fatalf("ModifyNetworkInterfaceAttribute: %v", err)
	}

	desc, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{id},
	})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	if got := aws.ToString(desc.NetworkInterfaces[0].Description); got != "after" {
		t.Errorf("description after modify = %q, want after", got)
	}
}

// TestDeleteMissingNetworkInterfaceCode pins that deleting an ENI that does not
// exist answers InvalidNetworkInterfaceID.NotFound, not a generic error.
func TestDeleteMissingNetworkInterfaceCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	_, err := c.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String("eni-deadbeef"),
	})
	if err == nil {
		t.Fatal("DeleteNetworkInterface(missing) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidNetworkInterfaceID.NotFound" {
		t.Errorf("code = %q, want InvalidNetworkInterfaceID.NotFound", got)
	}
}

// TestDeleteAttachedNetworkInterfaceCode pins that deleting an ENI attached to an
// instance answers InvalidNetworkInterface.InUse, not DependencyViolation.
func TestDeleteAttachedNetworkInterfaceCode(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	run, err := c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String("ami-12345"), InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1), SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	instanceID := aws.ToString(run.Instances[0].InstanceId)

	created, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	eniID := aws.ToString(created.NetworkInterface.NetworkInterfaceId)

	if _, err := c.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniID),
		InstanceId:         aws.String(instanceID),
		DeviceIndex:        aws.Int32(1),
	}); err != nil {
		t.Fatalf("AttachNetworkInterface: %v", err)
	}

	_, err = c.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniID),
	})
	if err == nil {
		t.Fatal("DeleteNetworkInterface(attached) succeeded, want an error")
	}

	if got := apiCode(t, err); got != "InvalidNetworkInterface.InUse" {
		t.Errorf("code = %q, want InvalidNetworkInterface.InUse", got)
	}
}
