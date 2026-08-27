package vpc

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func mkVPC(t *testing.T, m *Mock) *driver.VPCInfo {
	t.Helper()

	v, err := m.CreateVPC(context.Background(), driver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	return v
}

func mainRouteTable(t *testing.T, m *Mock, vpcID string) driver.RouteTable {
	t.Helper()

	rts, err := m.DescribeRouteTables(context.Background(), nil)
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	for _, rt := range rts {
		if rt.VPCID != vpcID {
			continue
		}

		for _, a := range rt.Associations {
			if a.Main {
				return rt
			}
		}
	}

	t.Fatalf("vpc %s has no main route table: %+v", vpcID, rts)

	return driver.RouteTable{}
}

// EC2 gives every VPC a route table at creation, carrying the local route and
// an implicit main association with no subnet.
func TestCreateVPCCreatesMainRouteTable(t *testing.T) {
	m := New(config.NewOptions())
	v := mkVPC(t, m)

	rt := mainRouteTable(t, m, v.ID)

	if len(rt.Routes) != 1 || rt.Routes[0].TargetType != RouteTargetLocal {
		t.Errorf("main route table should carry the local route, got %+v", rt.Routes)
	}

	if rt.Routes[0].DestinationCIDR != "10.0.0.0/16" {
		t.Errorf("local route CIDR = %q, want the VPC CIDR", rt.Routes[0].DestinationCIDR)
	}

	// The main association is implicit: it belongs to the VPC, not a subnet.
	if rt.Associations[0].SubnetID != "" {
		t.Errorf("main association should carry no subnet, got %q", rt.Associations[0].SubnetID)
	}
}

// A caller sweeping a VPC's route tables must skip the main one; real EC2
// refuses to delete it, and treating that refusal as a broken teardown would
// abandon everything downstream.
func TestMainRouteTableCannotBeDeletedDirectly(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())
	v := mkVPC(t, m)

	rt := mainRouteTable(t, m, v.ID)

	if err := m.DeleteRouteTable(ctx, rt.ID); err == nil {
		t.Error("deleting the main route table should fail")
	}

	if err := m.DisassociateRouteTable(ctx, rt.Associations[0].ID); err == nil {
		t.Error("disassociating the main association should fail")
	}
}

// It is implicit in the VPC, so it goes with the VPC — otherwise it would
// strand a row nothing can address.
func TestMainRouteTableDiesWithVPC(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())
	v := mkVPC(t, m)

	if err := m.DeleteVPC(ctx, v.ID); err != nil {
		t.Fatalf("DeleteVPC: %v", err)
	}

	rts, err := m.DescribeRouteTables(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeRouteTables: %v", err)
	}

	for _, rt := range rts {
		if rt.VPCID == v.ID {
			t.Errorf("route table %s outlived its VPC", rt.ID)
		}
	}
}

// A caller-created table is ordinary: deletable, and its association is too.
func TestNonMainRouteTableIsUnaffected(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())
	v := mkVPC(t, m)

	rt, err := m.CreateRouteTable(ctx, driver.RouteTableConfig{VPCID: v.ID})
	if err != nil {
		t.Fatalf("CreateRouteTable: %v", err)
	}

	sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	assoc, err := m.AssociateRouteTable(ctx, rt.ID, sub.ID)
	if err != nil {
		t.Fatalf("AssociateRouteTable: %v", err)
	}

	if assoc.Main {
		t.Error("a subnet association is never main")
	}

	if err := m.DisassociateRouteTable(ctx, assoc.ID); err != nil {
		t.Errorf("DisassociateRouteTable: %v", err)
	}

	if err := m.DeleteRouteTable(ctx, rt.ID); err != nil {
		t.Errorf("DeleteRouteTable: %v", err)
	}
}

// The drain a caller performs before deleting a network is only meaningful if
// skipping it fails. Real EC2 refuses a VPC delete while an interface is still
// attached, and accepting it here would let a broken drain pass unnoticed —
// the emulator agreeing with a caller the cloud would reject.
func TestDeleteRefusedWhileAnInterfaceIsAttached(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())
	v := mkVPC(t, m)

	sub, err := m.CreateSubnet(ctx, driver.SubnetConfig{VPCID: v.ID, CIDRBlock: "10.0.1.0/24"})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	// A NAT gateway holds an interface for as long as it lives. A private one
	// needs no Elastic IP, keeping this teardown check focused on the ENI it holds.
	if _, err := m.CreateNATGateway(ctx, driver.NATGatewayConfig{SubnetID: sub.ID, ConnectivityType: "private"}); err != nil {
		t.Fatalf("CreateNATGateway: %v", err)
	}

	if err := m.DeleteSubnet(ctx, sub.ID); err == nil {
		t.Error("subnet delete should be refused while an interface is attached")
	}

	if err := m.DeleteVPC(ctx, v.ID); err == nil {
		t.Error("vpc delete should be refused while an interface is attached")
	}

	// Draining releases the interface, and the deletes then succeed — the
	// refusal has to be recoverable or it is just a wall.
	enis, err := m.DescribeNetworkInterfaces(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeNetworkInterfaces: %v", err)
	}

	for i := range enis {
		if err := m.DetachNetworkInterface(ctx, enis[i].AttachmentID, true); err != nil {
			t.Fatalf("DetachNetworkInterface: %v", err)
		}

		if err := m.DeleteNetworkInterface(ctx, enis[i].ID); err != nil {
			t.Fatalf("DeleteNetworkInterface: %v", err)
		}
	}

	if err := m.DeleteSubnet(ctx, sub.ID); err != nil {
		t.Errorf("subnet delete after drain: %v", err)
	}

	if err := m.DeleteVPC(ctx, v.ID); err != nil {
		t.Errorf("vpc delete after drain: %v", err)
	}
}
