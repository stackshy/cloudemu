package memorydb

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

const oneYearSeconds = 31536000

// staticOfferings is the fixed reserved-node offering catalog.
//
//nolint:gochecknoglobals // immutable offering catalog.
var staticOfferings = []mdbdriver.ReservedNodesOffering{
	{
		OfferingID: "offer-r7g-large-1yr-noupfront", NodeType: "db.r7g.large",
		Duration: oneYearSeconds, FixedPrice: 0, OfferingType: "No Upfront",
	},
	{
		OfferingID: "offer-r7g-large-1yr-allupfront", NodeType: "db.r7g.large",
		Duration: oneYearSeconds, FixedPrice: 1200, OfferingType: "All Upfront",
	},
	{
		OfferingID: "offer-r7g-xlarge-1yr-allupfront", NodeType: "db.r7g.xlarge",
		Duration: oneYearSeconds, FixedPrice: 2400, OfferingType: "All Upfront",
	},
}

// DescribeReservedNodes returns purchased reserved nodes.
func (m *Mock) DescribeReservedNodes(_ context.Context) ([]mdbdriver.ReservedNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.reservedNodes.SortedValues()

	return append([]mdbdriver.ReservedNode(nil), all...), nil
}

// DescribeReservedNodesOfferings returns the static offering catalog.
func (*Mock) DescribeReservedNodesOfferings(_ context.Context) ([]mdbdriver.ReservedNodesOffering, error) {
	return append([]mdbdriver.ReservedNodesOffering(nil), staticOfferings...), nil
}

// PurchaseReservedNodesOffering records a reserved-node purchase.
func (m *Mock) PurchaseReservedNodesOffering(
	_ context.Context, offeringID, reservationID string, nodeCount int,
) (*mdbdriver.ReservedNode, error) {
	var offering *mdbdriver.ReservedNodesOffering

	for i := range staticOfferings {
		if staticOfferings[i].OfferingID == offeringID {
			offering = &staticOfferings[i]
			break
		}
	}

	if offering == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "reserved-nodes offering %q not found", offeringID)
	}

	if nodeCount <= 0 {
		nodeCount = 1
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if reservationID == "" {
		reservationID = "rn-" + offeringID
	}

	if m.reservedNodes.Has(reservationID) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "reservation %q already exists", reservationID)
	}

	rn := mdbdriver.ReservedNode{
		ReservationID: reservationID, OfferingID: offeringID, ReservedNodesOfferingID: offeringID,
		NodeType: offering.NodeType, NodeCount: nodeCount, Duration: offering.Duration,
		FixedPrice: offering.FixedPrice, OfferingType: offering.OfferingType,
		State: "active", StartTime: m.opts.Clock.Now().UTC(),
	}
	m.reservedNodes.Set(reservationID, rn)

	out := rn

	return &out, nil
}
