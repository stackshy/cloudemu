package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Peering statuses of a local peering gateway. A gateway is NEW until it is
// connected and REVOKED once the gateway on the far end is torn down.
const (
	PeeringNew     = "NEW"
	PeeringPeered  = "PEERED"
	PeeringRevoked = "REVOKED"
)

type lpgData struct {
	ID    string
	VCNID string
	// PeerID is the gateway on the far end, PeeringID the connection between
	// the two VCNs that stands for the pair in the portable projection.
	PeerID        string
	PeeringID     string
	PeeringStatus string
	Tags          map[string]string
}

// LocalPeeringGateway is one end of a VCN-to-VCN peering. The portable driver
// models the connection between two VCNs but neither gateway either side of
// it, so the handler reads this.
type LocalPeeringGateway struct {
	ID                  string
	VCNID               string
	PeerID              string
	PeeringStatus       string
	PeerAdvertisedCIDRs []string
	Tags                map[string]string
}

// CreateLocalPeeringGateway creates an unconnected gateway in a VCN.
func (m *Mock) CreateLocalPeeringGateway(
	_ context.Context, vcnID string, tags map[string]string,
) (*LocalPeeringGateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if vcnID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if !m.vcns.Has(vcnID) {
		return nil, vcnNotFound(vcnID)
	}

	id := m.newOCID(typeLocalPeering)
	g := &lpgData{
		ID:            id,
		VCNID:         vcnID,
		PeeringStatus: PeeringNew,
		Tags:          copyTags(tags),
	}

	m.lpgs.Set(id, g)
	m.record(id)

	info := m.toLPGInfo(g)

	return &info, nil
}

// DeleteLocalPeeringGateway deletes a gateway. Tearing down one end terminates
// the peering and leaves the far end revoked, as OCI does.
func (m *Mock) DeleteLocalPeeringGateway(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.lpgs.Get(id)
	if !ok {
		return lpgNotFound(id)
	}

	if g.PeeringID != "" {
		m.peerings.Delete(g.PeeringID)
		m.forget(g.PeeringID)
	}

	if g.PeerID != "" {
		m.revokeLPG(g.PeerID)
	}

	m.lpgs.Delete(id)
	m.forget(id)

	return nil
}

// DescribeLocalPeeringGateways returns gateways matching the given OCIDs, or
// all if empty.
func (m *Mock) DescribeLocalPeeringGateways(_ context.Context, ids []string) ([]LocalPeeringGateway, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.lpgs, ids, m.toLPGInfo), nil
}

// ConnectLocalPeeringGateways peers two gateways, which is what OCI's connect
// action does. Both ends move to PEERED and the connection between their VCNs
// becomes the portable projection of the pair.
func (m *Mock) ConnectLocalPeeringGateways(_ context.Context, id, peerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.lpgs.Get(id)
	if !ok {
		return lpgNotFound(id)
	}

	peer, ok := m.lpgs.Get(peerID)
	if !ok {
		return lpgNotFound(peerID)
	}

	if g.VCNID == peer.VCNID {
		return cerrors.New(cerrors.InvalidArgument, "a VCN cannot peer with itself")
	}

	for _, side := range []*lpgData{g, peer} {
		if side.PeeringStatus != PeeringNew {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"local peering gateway %q is %s, expected %s", side.ID, side.PeeringStatus, PeeringNew)
		}
	}

	// createPeering is the unlocked half of CreatePeeringConnection; calling
	// the exported method here would re-enter m.mu. The connect is itself the
	// acceptance, so the pair never sits pending.
	p, err := m.createPeering(
		driver.PeeringConfig{RequesterVPC: g.VCNID, AccepterVPC: peer.VCNID}, PeeringStatusActive)
	if err != nil {
		return err
	}

	m.linkLPG(id, peerID, p.ID)
	m.linkLPG(peerID, id, p.ID)

	return nil
}

// linkLPG points one gateway at its peer and the connection joining them.
func (m *Mock) linkLPG(id, peerID, peeringID string) {
	m.lpgs.Update(id, func(g *lpgData) *lpgData {
		g.PeerID = peerID
		g.PeeringID = peeringID
		g.PeeringStatus = PeeringPeered

		return g
	})
}

// revokeLPG drops a gateway's peering, which is the state OCI leaves the
// surviving end of a torn-down peering in.
func (m *Mock) revokeLPG(id string) {
	m.lpgs.Update(id, func(g *lpgData) *lpgData {
		g.PeerID = ""
		g.PeeringID = ""
		g.PeeringStatus = PeeringRevoked

		return g
	})
}

func lpgNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "local peering gateway %q not found", id)
}

// toLPGInfo projects a gateway, resolving the CIDR blocks the far end
// advertises across the peering.
func (m *Mock) toLPGInfo(g *lpgData) LocalPeeringGateway {
	out := LocalPeeringGateway{
		ID:            g.ID,
		VCNID:         g.VCNID,
		PeerID:        g.PeerID,
		PeeringStatus: g.PeeringStatus,
		Tags:          copyTags(g.Tags),
	}

	peer, ok := m.lpgs.Get(g.PeerID)
	if !ok {
		return out
	}

	if v, ok := m.vcns.Get(peer.VCNID); ok {
		out.PeerAdvertisedCIDRs = copyStringSlice(v.CIDRBlocks)
	}

	return out
}
