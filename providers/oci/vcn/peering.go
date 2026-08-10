package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Local peering states, named as the portable driver names them.
const (
	PeeringStatusPending  = "pending-acceptance"
	PeeringStatusActive   = "active"
	PeeringStatusRejected = "rejected"
)

// A peering connection is OCI's pair of connected local peering gateways.
// The gateways have no home in the portable driver, so this is what a portable
// caller sees; the wire layer serves the gateways either side of it.
type peeringData struct {
	ID           string
	RequesterVCN string
	AccepterVCN  string
	Status       string
	CreatedAt    string
	Tags         map[string]string
}

// CreatePeeringConnection connects two VCNs. Real OCI refuses overlapping
// CIDR blocks, since the peer's addresses would be unroutable.
func (m *Mock) CreatePeeringConnection(_ context.Context, cfg driver.PeeringConfig) (*driver.PeeringConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, err := m.createPeering(cfg, PeeringStatusPending)
	if err != nil {
		return nil, err
	}

	info := toPeeringInfo(p)

	return &info, nil
}

// createPeering is CreatePeeringConnection without the lock, so a caller
// already holding m.mu — connecting a pair of local peering gateways — can
// reach it without re-entering. The peering OCID carries OCI's local peering
// gateway type segment, which is what a route rule points at.
func (m *Mock) createPeering(cfg driver.PeeringConfig, status string) (*peeringData, error) {
	if cfg.RequesterVPC == "" || cfg.AccepterVPC == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "both requester and accepter VCN OCIDs are required")
	}

	requester, ok := m.vcns.Get(cfg.RequesterVPC)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "requester VCN %q not found", cfg.RequesterVPC)
	}

	accepter, ok := m.vcns.Get(cfg.AccepterVPC)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "accepter VCN %q not found", cfg.AccepterVPC)
	}

	if anyOverlap(requester.CIDRBlocks, accepter.CIDRBlocks) {
		return nil, cerrors.New(cerrors.InvalidArgument, "peered VCNs must not have overlapping CIDR blocks")
	}

	id := m.newOCID(typeLocalPeering)
	p := &peeringData{
		ID:           id,
		RequesterVCN: cfg.RequesterVPC,
		AccepterVCN:  cfg.AccepterVPC,
		Status:       status,
		CreatedAt:    m.now(),
		Tags:         copyTags(cfg.Tags),
	}

	m.peerings.Set(id, p)
	m.record(id)

	return p, nil
}

// AcceptPeeringConnection accepts a pending peering.
func (m *Mock) AcceptPeeringConnection(_ context.Context, peeringID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.setPeeringStatus(peeringID, PeeringStatusActive)
}

// RejectPeeringConnection rejects a pending peering.
func (m *Mock) RejectPeeringConnection(_ context.Context, peeringID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.setPeeringStatus(peeringID, PeeringStatusRejected)
}

// setPeeringStatus moves a pending peering to a terminal status.
func (m *Mock) setPeeringStatus(peeringID, status string) error {
	notFound := cerrors.Newf(cerrors.NotFound, "peering %q not found", peeringID)

	return mutate(m.peerings, peeringID, notFound, func(p *peeringData) error {
		if p.Status != PeeringStatusPending {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"peering %q is in state %q, expected %q", peeringID, p.Status, PeeringStatusPending)
		}

		p.Status = status

		return nil
	})
}

// DeletePeeringConnection deletes a peering, revoking the gateways either side
// of it so neither is left pointing at a connection that is gone.
func (m *Mock) DeletePeeringConnection(_ context.Context, peeringID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.peerings.Delete(peeringID) {
		return cerrors.Newf(cerrors.NotFound, "peering %q not found", peeringID)
	}

	for id, g := range m.lpgs.All() {
		if g.PeeringID == peeringID {
			m.revokeLPG(id)
		}
	}

	m.forget(peeringID)

	return nil
}

// DescribePeeringConnections returns peerings matching the given OCIDs, or
// all if empty.
func (m *Mock) DescribePeeringConnections(_ context.Context, ids []string) ([]driver.PeeringConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.peerings, ids, toPeeringInfo), nil
}

func toPeeringInfo(p *peeringData) driver.PeeringConnection {
	return driver.PeeringConnection{
		ID:           p.ID,
		RequesterVPC: p.RequesterVCN,
		AccepterVPC:  p.AccepterVCN,
		Status:       p.Status,
		CreatedAt:    p.CreatedAt,
		Tags:         copyTags(p.Tags),
	}
}
