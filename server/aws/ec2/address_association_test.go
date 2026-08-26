package ec2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// TestAssociateAddressAllowReassociation pins the AllowReassociation contract:
// reassociation is automatic by default, but an explicit AllowReassociation=false
// makes a re-associate onto a different target fail with Resource.AlreadyAssociated,
// while AllowReassociation=true moves the EIP to the new interface.
func TestAssociateAddressAllowReassociation(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	_, subnetID := mkVPCSubnet(t, c)

	mkENI := func() string {
		eni, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
			SubnetId: aws.String(subnetID),
		})
		if err != nil {
			t.Fatalf("CreateNetworkInterface: %v", err)
		}

		return aws.ToString(eni.NetworkInterface.NetworkInterfaceId)
	}

	eni1 := mkENI()
	eni2 := mkENI()

	alloc, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}

	if _, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		NetworkInterfaceId: aws.String(eni1),
	}); err != nil {
		t.Fatalf("AssociateAddress to eni1: %v", err)
	}

	_, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		NetworkInterfaceId: aws.String(eni2),
		AllowReassociation: aws.Bool(false),
	})
	if err == nil {
		t.Fatal("re-associate with AllowReassociation=false succeeded, want Resource.AlreadyAssociated")
	}

	if code := apiCode(t, err); code != "Resource.AlreadyAssociated" {
		t.Errorf("error code = %q, want Resource.AlreadyAssociated", code)
	}

	if _, err = c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       alloc.AllocationId,
		NetworkInterfaceId: aws.String(eni2),
		AllowReassociation: aws.Bool(true),
	}); err != nil {
		t.Fatalf("re-associate with AllowReassociation=true: %v", err)
	}

	desc, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{aws.ToString(alloc.AllocationId)},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses: %v", err)
	}

	if got := aws.ToString(desc.Addresses[0].NetworkInterfaceId); got != eni2 {
		t.Errorf("after reassociation networkInterfaceId = %q, want %q", got, eni2)
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

// TestDescribeAddressesFilters pins that the allocation-id / public-ip /
// association-id / domain filters narrow the result instead of returning every
// address, that a non-matching filter yields an empty set, and that an explicit
// allocation-id list still resolves.
func TestDescribeAddressesFilters(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)

	_, subnetID := mkVPCSubnet(t, c)

	eni, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{SubnetId: aws.String(subnetID)})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	eip1, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress(1): %v", err)
	}

	assoc, err := c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       eip1.AllocationId,
		NetworkInterfaceId: eni.NetworkInterface.NetworkInterfaceId,
	})
	if err != nil {
		t.Fatalf("AssociateAddress: %v", err)
	}

	eip2, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{})
	if err != nil {
		t.Fatalf("AllocateAddress(2): %v", err)
	}

	byAlloc, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{{Name: aws.String("allocation-id"), Values: []string{aws.ToString(eip1.AllocationId)}}},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses(allocation-id): %v", err)
	}

	if len(byAlloc.Addresses) != 1 || aws.ToString(byAlloc.Addresses[0].AllocationId) != aws.ToString(eip1.AllocationId) {
		t.Fatalf("allocation-id filter = %d addresses, want only %s", len(byAlloc.Addresses), aws.ToString(eip1.AllocationId))
	}

	byIP, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{{Name: aws.String("public-ip"), Values: []string{aws.ToString(eip2.PublicIp)}}},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses(public-ip): %v", err)
	}

	if len(byIP.Addresses) != 1 || aws.ToString(byIP.Addresses[0].PublicIp) != aws.ToString(eip2.PublicIp) {
		t.Fatalf("public-ip filter = %d addresses, want only %s", len(byIP.Addresses), aws.ToString(eip2.PublicIp))
	}

	byAssoc, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{{Name: aws.String("association-id"), Values: []string{aws.ToString(assoc.AssociationId)}}},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses(association-id): %v", err)
	}

	if len(byAssoc.Addresses) != 1 || aws.ToString(byAssoc.Addresses[0].AllocationId) != aws.ToString(eip1.AllocationId) {
		t.Fatalf("association-id filter = %d addresses, want only eip1", len(byAssoc.Addresses))
	}

	byDomain, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{{Name: aws.String("domain"), Values: []string{"vpc"}}},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses(domain): %v", err)
	}

	if len(byDomain.Addresses) != 2 {
		t.Fatalf("domain=vpc filter = %d addresses, want both (2)", len(byDomain.Addresses))
	}

	none, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{{Name: aws.String("allocation-id"), Values: []string{"eipalloc-nope"}}},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses(bogus): %v", err)
	}

	if len(none.Addresses) != 0 {
		t.Fatalf("non-matching filter returned %d addresses, want 0", len(none.Addresses))
	}

	byList, err := c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{AllocationIds: []string{aws.ToString(eip2.AllocationId)}})
	if err != nil {
		t.Fatalf("DescribeAddresses(id list): %v", err)
	}

	if len(byList.Addresses) != 1 || aws.ToString(byList.Addresses[0].AllocationId) != aws.ToString(eip2.AllocationId) {
		t.Fatalf("id list = %d addresses, want only %s", len(byList.Addresses), aws.ToString(eip2.AllocationId))
	}
}
