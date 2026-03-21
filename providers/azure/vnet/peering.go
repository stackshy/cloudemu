package vnet

import (
	"context"
	"fmt"
	"net"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

// Peering status constants.
const (
	PeeringStatusPending  = "pending-acceptance"
	PeeringStatusActive   = "active"
	PeeringStatusRejected = "rejected"
	PeeringStatusDeleted  = "deleted"
)

type peeringData struct {
	ID           string
	RequesterVPC string
	AccepterVPC  string
	Status       string
	CreatedAt    string
	Tags         map[string]string
}

// CreatePeeringConnection creates a VNet peering between two virtual networks.
func (m *Mock) CreatePeeringConnection(
	_ context.Context, cfg driver.PeeringConfig,
) (*driver.PeeringConnection, error) {
	if cfg.RequesterVPC == "" || cfg.AccepterVPC == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "both requester and accepter VNet IDs are required")
	}

	reqVPC, ok := m.vpcs.Get(cfg.RequesterVPC)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "requester vnet %q not found", cfg.RequesterVPC)
	}

	accVPC, ok := m.vpcs.Get(cfg.AccepterVPC)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "accepter vnet %q not found", cfg.AccepterVPC)
	}

	if cidrsOverlap(reqVPC.CIDRBlock, accVPC.CIDRBlock) {
		return nil, cerrors.New(cerrors.InvalidArgument, "VNet CIDRs must not overlap for peering")
	}

	id := idgen.GenerateID("peering-")
	p := &peeringData{
		ID:           id,
		RequesterVPC: cfg.RequesterVPC,
		AccepterVPC:  cfg.AccepterVPC,
		Status:       PeeringStatusPending,
		CreatedAt:    m.opts.Clock.Now().Format(timeFormat),
		Tags:         copyTags(cfg.Tags),
	}
	m.peerings.Set(id, p)

	info := toPeeringInfo(p)

	return &info, nil
}

// AcceptPeeringConnection accepts a pending VNet peering.
func (m *Mock) AcceptPeeringConnection(_ context.Context, peeringID string) error {
	p, ok := m.peerings.Get(peeringID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "peering %q not found", peeringID)
	}

	if p.Status != PeeringStatusPending {
		return cerrors.Newf(cerrors.FailedPrecondition, "peering %q is in state %q, expected %q",
			peeringID, p.Status, PeeringStatusPending)
	}

	p.Status = PeeringStatusActive

	return nil
}

// RejectPeeringConnection rejects a pending VNet peering.
func (m *Mock) RejectPeeringConnection(_ context.Context, peeringID string) error {
	p, ok := m.peerings.Get(peeringID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "peering %q not found", peeringID)
	}

	if p.Status != PeeringStatusPending {
		return cerrors.Newf(cerrors.FailedPrecondition, "peering %q is in state %q, expected %q",
			peeringID, p.Status, PeeringStatusPending)
	}

	p.Status = PeeringStatusRejected

	return nil
}

// DeletePeeringConnection deletes a VNet peering.
func (m *Mock) DeletePeeringConnection(_ context.Context, peeringID string) error {
	p, ok := m.peerings.Get(peeringID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "peering %q not found", peeringID)
	}

	p.Status = PeeringStatusDeleted

	m.peerings.Delete(peeringID)

	return nil
}

// DescribePeeringConnections returns VNet peerings matching the given IDs.
func (m *Mock) DescribePeeringConnections(
	_ context.Context, ids []string,
) ([]driver.PeeringConnection, error) {
	return describeResources(m.peerings, ids, toPeeringInfo), nil
}

func toPeeringInfo(p *peeringData) driver.PeeringConnection {
	return driver.PeeringConnection{
		ID:           p.ID,
		RequesterVPC: p.RequesterVPC,
		AccepterVPC:  p.AccepterVPC,
		Status:       p.Status,
		CreatedAt:    p.CreatedAt,
		Tags:         copyTags(p.Tags),
	}
}

// cidrsOverlap checks whether two CIDR blocks overlap.
func cidrsOverlap(cidrA, cidrB string) bool {
	_, netA, errA := net.ParseCIDR(cidrA)
	_, netB, errB := net.ParseCIDR(cidrB)

	if errA != nil || errB != nil {
		return cidrA == cidrB
	}

	return netA.Contains(netB.IP) || netB.Contains(netA.IP)
}

// mockPublicIP generates a mock public IP from a counter value.
func mockPublicIP(id string) string {
	var sum int
	for _, c := range id {
		sum += int(c)
	}

	octet3 := sum % maxOctetValue
	octet4 := (sum / maxOctetValue) % maxOctetValue

	return fmt.Sprintf("10.0.%d.%d", octet3, octet4)
}
