package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

func apiCode(t *testing.T, err error) string {
	t.Helper()
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an API error, got %v", err)
	}
	return apiErr.ErrorCode()
}

// TestDeletionGuardsMatchAWS pins the real-AWS deletion/dependency semantics the
// e2e audit found missing: a security group in use by an ENI, a route table with
// a subnet association (and the main route table), and an Elastic IP held by a
// NAT gateway all refuse deletion with the correct error code.
func TestDeletionGuardsMatchAWS(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, subnetID := mkVPCSubnet(t, c)

	// (1) SecurityGroup in use by an ENI -> DependencyViolation.
	sg, err := c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String("g1"), Description: aws.String("d"), VpcId: aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := aws.ToString(sg.GroupId)

	eni, err := c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID), Groups: []string{sgID},
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	if _, err := c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}); err == nil {
		t.Fatal("DeleteSecurityGroup of an in-use SG should fail")
	} else if code := apiCode(t, err); code != "DependencyViolation" {
		t.Fatalf("DeleteSecurityGroup(in-use) code = %q, want DependencyViolation", code)
	}
	// After removing the ENI, the SG deletes.
	if _, err := c.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: eni.NetworkInterface.NetworkInterfaceId,
	}); err != nil {
		t.Fatalf("DeleteNetworkInterface: %v", err)
	}
	if _, err := c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}); err != nil {
		t.Fatalf("DeleteSecurityGroup after ENI removed: %v", err)
	}

	// (2) Route table with a subnet association -> DependencyViolation.
	rt, err := c.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{VpcId: aws.String(vpcID)})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}
	rtID := aws.ToString(rt.RouteTable.RouteTableId)
	assoc, err := c.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtID), SubnetId: aws.String(subnetID),
	})
	if err != nil {
		t.Fatalf("AssociateRouteTable: %v", err)
	}
	if _, err := c.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: aws.String(rtID)}); err == nil {
		t.Fatal("DeleteRouteTable with a subnet association should fail")
	} else if code := apiCode(t, err); code != "DependencyViolation" {
		t.Fatalf("DeleteRouteTable(associated) code = %q, want DependencyViolation", code)
	}
	// Disassociate, then it deletes.
	if _, err := c.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{AssociationId: assoc.AssociationId}); err != nil {
		t.Fatalf("DisassociateRouteTable: %v", err)
	}
	if _, err := c.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: aws.String(rtID)}); err != nil {
		t.Fatalf("DeleteRouteTable after disassociate: %v", err)
	}

	// (3) Main route table -> DependencyViolation.
	rts, err := c.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}
	var mainRT string
	for _, r := range rts.RouteTables {
		for _, a := range r.Associations {
			if aws.ToBool(a.Main) {
				mainRT = aws.ToString(r.RouteTableId)
			}
		}
	}
	if mainRT == "" {
		t.Fatal("no main route table found")
	}
	if _, err := c.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: aws.String(mainRT)}); err == nil {
		t.Fatal("DeleteRouteTable of the main table should fail")
	} else if code := apiCode(t, err); code != "DependencyViolation" {
		t.Fatalf("DeleteRouteTable(main) code = %q, want DependencyViolation", code)
	}

	// (4) Elastic IP held by a NAT gateway -> InvalidIPAddress.InUse.
	eip, err := c.AllocateAddress(ctx, &ec2.AllocateAddressInput{Domain: ec2types.DomainTypeVpc})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}
	nat, err := c.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		SubnetId: aws.String(subnetID), AllocationId: eip.AllocationId,
	})
	if err != nil {
		t.Fatalf("CreateNatGateway: %v", err)
	}
	if _, err := c.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: eip.AllocationId}); err == nil {
		t.Fatal("ReleaseAddress of an in-use EIP should fail")
	} else if code := apiCode(t, err); code != "InvalidIPAddress.InUse" {
		t.Fatalf("ReleaseAddress(in-use) code = %q, want InvalidIPAddress.InUse", code)
	}
	// After the NAT gateway is gone, the EIP releases.
	if _, err := c.DeleteNatGateway(ctx, &ec2.DeleteNatGatewayInput{NatGatewayId: nat.NatGateway.NatGatewayId}); err != nil {
		t.Fatalf("DeleteNatGateway: %v", err)
	}
	if _, err := c.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: eip.AllocationId}); err != nil {
		t.Fatalf("ReleaseAddress after NAT deleted: %v", err)
	}
}
