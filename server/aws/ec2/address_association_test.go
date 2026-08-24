package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// TestAssociateAddressToNetworkInterface pins the EIP-on-ENI flow (Terraform
// aws_eip_association targeting a network interface, e.g. a NAT instance's
// secondary ENI). AssociateAddress binds the allocation to a NetworkInterfaceId
// with an optional PrivateIpAddress, and DescribeAddresses reports the
// networkInterfaceId, privateIpAddress and networkInterfaceOwnerId back.
func TestAssociateAddressToNetworkInterface(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	eni, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	eniID := aws.ToString(eni.NetworkInterface.NetworkInterfaceId)

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	assoc, err := c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		NetworkInterfaceId: aws.String(eniID),
		PrivateIpAddress:   aws.String("10.0.1.55"),
	})
	if err != nil {
		t.Fatalf("AssociateAddress: %v", err)
	}

	if aws.ToString(assoc.AssociationId) == "" {
		t.Fatal("AssociateAddress returned an empty associationId")
	}

	desc, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{aws.ToString(alloc.AllocationId)},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses: %v", err)
	}

	if len(desc.Addresses) != 1 {
		t.Fatalf("DescribeAddresses returned %d addresses, want 1", len(desc.Addresses))
	}

	a := desc.Addresses[0]

	if got := aws.ToString(a.NetworkInterfaceId); got != eniID {
		t.Errorf("networkInterfaceId = %q, want %q", got, eniID)
	}

	if got := aws.ToString(a.PrivateIpAddress); got != "10.0.1.55" {
		t.Errorf("privateIpAddress = %q, want 10.0.1.55", got)
	}

	if got := aws.ToString(a.NetworkInterfaceOwnerId); got != "123456789012" {
		t.Errorf("networkInterfaceOwnerId = %q, want 123456789012", got)
	}

	if got := aws.ToString(a.AssociationId); got != aws.ToString(assoc.AssociationId) {
		t.Errorf("DescribeAddresses associationId = %q, want %q", got, aws.ToString(assoc.AssociationId))
	}
}

// TestAssociateAddressUnknownNetworkInterface pins that binding an EIP to a
// nonexistent network interface fails with InvalidNetworkInterfaceID.NotFound
// rather than reporting a phantom success.
func TestAssociateAddressUnknownNetworkInterface(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	_, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		NetworkInterfaceId: aws.String("eni-00000000000000000"),
	})
	if err == nil {
		t.Fatal("AssociateAddress to unknown ENI succeeded, want InvalidNetworkInterfaceID.NotFound")
	}

	if code := apiCode(t, err); code != "InvalidNetworkInterfaceID.NotFound" {
		t.Errorf("error code = %q, want InvalidNetworkInterfaceID.NotFound", code)
	}
}

// TestAssociateAddressUnknownInstance pins that a typo'd or nonexistent
// InstanceId is rejected with InvalidInstanceID.NotFound instead of being
// silently accepted against a target that does not exist.
func TestAssociateAddressUnknownInstance(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	_, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: alloc.AllocationId,
		InstanceId:   aws.String("i-00000000000000000"),
	})
	if err == nil {
		t.Fatal("AssociateAddress to unknown instance succeeded, want InvalidInstanceID.NotFound")
	}

	if code := apiCode(t, err); code != "InvalidInstanceID.NotFound" {
		t.Errorf("error code = %q, want InvalidInstanceID.NotFound", code)
	}
}

// TestAssociateAddressInstanceAndENIExclusive pins that specifying both an
// InstanceId and a NetworkInterfaceId is rejected — real EC2 accepts one or the
// other, not both.
func TestAssociateAddressInstanceAndENIExclusive(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	_, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		InstanceId:         aws.String("i-00000000000000000"),
		NetworkInterfaceId: aws.String("eni-00000000000000000"),
	})
	if err == nil {
		t.Fatal("AssociateAddress with both InstanceId and NetworkInterfaceId succeeded, want InvalidParameterCombination")
	}

	if code := apiCode(t, err); code != "InvalidParameterCombination" {
		t.Errorf("error code = %q, want InvalidParameterCombination", code)
	}
}
