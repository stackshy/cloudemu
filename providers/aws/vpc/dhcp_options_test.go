package vpc

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestDeleteDHCPOptionsAssociatedIsDependencyViolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vpc := createTestVPC(m)

	opt, err := m.CreateDHCPOptions(ctx, driver.DHCPOptionsConfig{
		Configuration: map[string][]string{"domain-name-servers": {"10.0.0.2"}},
	})
	if err != nil {
		t.Fatalf("CreateDHCPOptions: %v", err)
	}

	if err := m.AssociateDHCPOptions(ctx, opt.ID, vpc.ID); err != nil {
		t.Fatalf("AssociateDHCPOptions: %v", err)
	}

	err = m.DeleteDHCPOptions(ctx, opt.ID)
	if !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete associated err = %v, want FailedPrecondition", err)
	}

	// Re-associate to default frees it.
	if err := m.AssociateDHCPOptions(ctx, "default", vpc.ID); err != nil {
		t.Fatalf("AssociateDHCPOptions(default): %v", err)
	}
	if err := m.DeleteDHCPOptions(ctx, opt.ID); err != nil {
		t.Fatalf("DeleteDHCPOptions after re-associate: %v", err)
	}
}

func TestDescribeDHCPOptionsUnknownNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.DescribeDHCPOptions(context.Background(), []string{"dopt-missing"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("unknown dhcp options err = %v, want NotFound", err)
	}
}

func TestDescribePeeringConnectionsUnknownNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.DescribePeeringConnections(context.Background(), []string{"pcx-missing"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("unknown peering err = %v, want NotFound", err)
	}
}

func TestAttachNetworkInterfaceRecordsAttachment(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vpc := createTestVPC(m)
	subnet, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	eni, err := m.CreateNetworkInterface(ctx, subnet.ID, "", nil, nil)
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}

	attachmentID, err := m.AttachNetworkInterface(ctx, eni.ID, "i-123", 1)
	if err != nil {
		t.Fatalf("AttachNetworkInterface: %v", err)
	}
	if attachmentID == "" {
		t.Fatal("AttachNetworkInterface returned empty attachment id")
	}

	// A second attach of the same in-use ENI is rejected.
	if _, err := m.AttachNetworkInterface(ctx, eni.ID, "i-456", 2); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("re-attach err = %v, want FailedPrecondition", err)
	}

	got, err := m.DescribeNetworkInterfaces(ctx, []string{eni.ID})
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}
	if got[0].Status != ENIStatusInUse || got[0].InstanceID != "i-123" {
		t.Fatalf("ENI after attach = %+v, want in-use on i-123", got[0])
	}
}
