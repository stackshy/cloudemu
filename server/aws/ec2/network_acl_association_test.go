package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"
)

// findSubnetAssociation returns the association id and acl id for a subnet by
// scanning every ACL's associationSet, the way a real user reads it.
func findSubnetAssociation(t *testing.T, c *ec2.Client, subnetID string) (assocID, aclID string) {
	t.Helper()

	out, err := c.DescribeNetworkAcls(context.Background(), &ec2.DescribeNetworkAclsInput{})
	if err != nil {
		t.Fatalf("DescribeNetworkAcls: %v", err)
	}

	for _, acl := range out.NetworkAcls {
		for _, a := range acl.Associations {
			if aws.ToString(a.SubnetId) == subnetID {
				return aws.ToString(a.NetworkAclAssociationId), aws.ToString(a.NetworkAclId)
			}
		}
	}

	return "", ""
}

// TestReplaceNetworkAclAssociationWire drives the real AWS SDK through the wire:
// a fresh subnet is associated with the VPC's default ACL, and
// ReplaceNetworkAclAssociation moves it to a custom ACL, yielding a new
// association id and flipping which ACL DescribeNetworkAcls reports for it.
func TestReplaceNetworkAclAssociationWire(t *testing.T) {
	ctx := context.Background()
	c := newRoutingEdgeEC2(t)
	vpcID, subnetID := mkVPCSubnet(t, c)

	// The subnet starts associated with its VPC's default ACL.
	assocID, defaultACLID := findSubnetAssociation(t, c, subnetID)
	if assocID == "" {
		t.Fatal("new subnet has no default network-ACL association")
	}

	// A custom ACL to move the subnet onto.
	custom, err := c.CreateNetworkAcl(ctx, &ec2.CreateNetworkAclInput{VpcId: aws.String(vpcID)})
	if err != nil {
		t.Fatalf("CreateNetworkAcl: %v", err)
	}
	customACLID := aws.ToString(custom.NetworkAcl.NetworkAclId)

	rep, err := c.ReplaceNetworkAclAssociation(ctx, &ec2.ReplaceNetworkAclAssociationInput{
		AssociationId: aws.String(assocID),
		NetworkAclId:  aws.String(customACLID),
	})
	if err != nil {
		t.Fatalf("ReplaceNetworkAclAssociation: %v", err)
	}

	newAssoc := aws.ToString(rep.NewAssociationId)
	if newAssoc == "" || newAssoc == assocID {
		t.Fatalf("newAssociationId = %q, want a fresh id != %q", newAssoc, assocID)
	}

	// The subnet is now on the custom ACL, no longer the default.
	gotAssoc, gotACL := findSubnetAssociation(t, c, subnetID)
	if gotACL != customACLID {
		t.Fatalf("subnet now on ACL %q, want %q", gotACL, customACLID)
	}
	if gotAssoc != newAssoc {
		t.Fatalf("subnet association = %q, want the new %q", gotAssoc, newAssoc)
	}
	if gotACL == defaultACLID {
		t.Fatal("subnet still reports the default ACL after replace")
	}

	// The old association id no longer exists.
	_, err = c.ReplaceNetworkAclAssociation(ctx, &ec2.ReplaceNetworkAclAssociationInput{
		AssociationId: aws.String(assocID),
		NetworkAclId:  aws.String(customACLID),
	})
	if err == nil {
		t.Fatal("replacing a stale association id should error")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidAssociationID.NotFound" {
		t.Fatalf("stale association error = %v, want InvalidAssociationID.NotFound", err)
	}

	// A bad target ACL id is a distinct error code.
	assoc2, _ := findSubnetAssociation(t, c, subnetID)
	_, err = c.ReplaceNetworkAclAssociation(ctx, &ec2.ReplaceNetworkAclAssociationInput{
		AssociationId: aws.String(assoc2),
		NetworkAclId:  aws.String("acl-does-not-exist"),
	})
	if err == nil {
		t.Fatal("replacing onto a missing ACL should error")
	}
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidNetworkAclID.NotFound" {
		t.Fatalf("bad ACL error = %v, want InvalidNetworkAclID.NotFound", err)
	}
}
