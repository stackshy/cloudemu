package route53

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// TestCreateZoneWithVPCs proves create-time VPC associations are stored and
// returned on GetZone, detached from the caller's slice.
func TestCreateZoneWithVPCs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	vpcs := []driver.VPCAssociation{{VPCID: "vpc-1", VPCRegion: "us-east-1"}}

	info, err := m.CreateZone(ctx, driver.ZoneConfig{Name: "internal.com", Private: true, VPCs: vpcs})
	requireNoError(t, err)
	assertEqual(t, 1, len(info.VPCs))

	// Mutating the caller's slice must not reach the store.
	vpcs[0].VPCID = "mutated"

	got, err := m.GetZone(ctx, info.ID)
	requireNoError(t, err)
	assertEqual(t, 1, len(got.VPCs))
	assertEqual(t, "vpc-1", got.VPCs[0].VPCID)
}

// TestAssociateVPCIdempotent proves associating adds a VPC and is a no-op when
// the VPC is already associated.
func TestAssociateVPCIdempotent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateZone(ctx, driver.ZoneConfig{
		Name:    "internal.com",
		Private: true,
		VPCs:    []driver.VPCAssociation{{VPCID: "vpc-1", VPCRegion: "us-east-1"}},
	})
	requireNoError(t, err)

	requireNoError(t, m.AssociateVPC(ctx, info.ID, driver.VPCAssociation{VPCID: "vpc-2", VPCRegion: "us-east-1"}))

	got, err := m.GetZone(ctx, info.ID)
	requireNoError(t, err)
	assertEqual(t, 2, len(got.VPCs))

	// Re-associating vpc-2 is idempotent.
	requireNoError(t, m.AssociateVPC(ctx, info.ID, driver.VPCAssociation{VPCID: "vpc-2", VPCRegion: "us-east-1"}))

	got, err = m.GetZone(ctx, info.ID)
	requireNoError(t, err)
	assertEqual(t, 2, len(got.VPCs))
}

// TestDisassociateVPC proves disassociating removes exactly the requested VPC.
func TestDisassociateVPC(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateZone(ctx, driver.ZoneConfig{
		Name:    "internal.com",
		Private: true,
		VPCs: []driver.VPCAssociation{
			{VPCID: "vpc-1", VPCRegion: "us-east-1"},
			{VPCID: "vpc-2", VPCRegion: "us-east-1"},
		},
	})
	requireNoError(t, err)

	requireNoError(t, m.DisassociateVPC(ctx, info.ID, driver.VPCAssociation{VPCID: "vpc-1", VPCRegion: "us-east-1"}))

	got, err := m.GetZone(ctx, info.ID)
	requireNoError(t, err)
	assertEqual(t, 1, len(got.VPCs))
	assertEqual(t, "vpc-2", got.VPCs[0].VPCID)
}

// TestAssociateVPCMissingZone proves a missing zone is a NotFound.
func TestAssociateVPCMissingZone(t *testing.T) {
	m := newTestMock()

	err := m.AssociateVPC(context.Background(), "ZNONE", driver.VPCAssociation{VPCID: "vpc-1", VPCRegion: "us-east-1"})
	assertError(t, err, true)
}
