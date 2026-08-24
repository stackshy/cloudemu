package vpc

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestNetworkACLAssociationLifecycle pins the default-ACL + association model:
// a new VPC gets a default ACL, a new subnet auto-associates with it,
// ReplaceNetworkACLAssociation moves the subnet (fresh id), the moved-onto ACL
// can't be deleted while associated, and the errors are NotFound-typed.
func TestNetworkACLAssociationLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vpc := createTestVPC(m)

	// The VPC has exactly one default ACL.
	acls, _ := m.DescribeNetworkACLs(ctx, nil)
	var defACL string
	defCount := 0
	for _, a := range acls {
		if a.VPCID == vpc.ID && a.IsDefault {
			defACL = a.ID
			defCount++
		}
	}
	if defCount != 1 {
		t.Fatalf("VPC has %d default ACLs, want 1", defCount)
	}

	// A new subnet auto-associates with the default ACL.
	sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	acls, _ = m.DescribeNetworkACLs(ctx, []string{defACL})
	if len(acls) != 1 || len(acls[0].Associations) != 1 || acls[0].Associations[0].SubnetID != sub.ID {
		t.Fatalf("default ACL associations = %+v, want one for subnet %s", acls[0].Associations, sub.ID)
	}
	assocID := acls[0].Associations[0].ID

	// A custom ACL and a move onto it.
	custom, _ := m.CreateNetworkACL(ctx, vpc.ID, nil)

	newAssoc, err := m.ReplaceNetworkACLAssociation(ctx, assocID, custom.ID)
	if err != nil {
		t.Fatalf("ReplaceNetworkACLAssociation: %v", err)
	}
	if newAssoc.ID == assocID || newAssoc.NetworkACLID != custom.ID || newAssoc.SubnetID != sub.ID {
		t.Fatalf("new association = %+v, want fresh id on custom ACL for the subnet", newAssoc)
	}

	// The default ACL no longer lists the subnet; the custom one does.
	acls, _ = m.DescribeNetworkACLs(ctx, []string{defACL})
	if len(acls[0].Associations) != 0 {
		t.Fatalf("default ACL still has associations: %+v", acls[0].Associations)
	}
	acls, _ = m.DescribeNetworkACLs(ctx, []string{custom.ID})
	if len(acls[0].Associations) != 1 || acls[0].Associations[0].ID != newAssoc.ID {
		t.Fatalf("custom ACL associations = %+v, want the new one", acls[0].Associations)
	}

	// The custom ACL is now un-deletable while associated.
	if err := m.DeleteNetworkACL(ctx, custom.ID); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("DeleteNetworkACL(associated) = %v, want FailedPrecondition", err)
	}

	// Error typing.
	if _, err := m.ReplaceNetworkACLAssociation(ctx, "aclassoc-missing", custom.ID); !cerrors.IsNotFound(err) {
		t.Fatalf("replace with bad association = %v, want NotFound", err)
	}
	if _, err := m.ReplaceNetworkACLAssociation(ctx, newAssoc.ID, "acl-missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("replace onto bad ACL = %v, want NotFound", err)
	}

	// Deleting the subnet clears its association.
	if err := m.DeleteSubnet(ctx, sub.ID); err != nil {
		t.Fatalf("DeleteSubnet: %v", err)
	}
	acls, _ = m.DescribeNetworkACLs(ctx, []string{custom.ID})
	if len(acls[0].Associations) != 0 {
		t.Fatalf("custom ACL still associated after subnet delete: %+v", acls[0].Associations)
	}
}
