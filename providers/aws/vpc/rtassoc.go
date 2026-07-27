package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type rtAssocData struct {
	ID           string
	RouteTableID string
	SubnetID     string
	// Main marks the VPC's implicit main-route-table association. It carries
	// no subnet and cannot be disassociated.
	Main bool
}

// AssociateRouteTable associates a route table with a subnet.
func (m *Mock) AssociateRouteTable(
	_ context.Context, routeTableID, subnetID string,
) (*driver.RouteTableAssociation, error) {
	if !m.routeTables.Has(routeTableID) {
		return nil, errors.Newf(
			errors.NotFound,
			"route table %q not found", routeTableID,
		)
	}

	if !m.subnets.Has(subnetID) {
		return nil, errors.Newf(
			errors.NotFound,
			"subnet %q not found", subnetID,
		)
	}

	id := idgen.GenerateID("rtbassoc-")

	assoc := &rtAssocData{
		ID:           id,
		RouteTableID: routeTableID,
		SubnetID:     subnetID,
	}
	m.rtAssocs.Set(id, assoc)

	info := toRTAssocInfo(assoc)

	return &info, nil
}

// DisassociateRouteTable removes a route table association.
func (m *Mock) DisassociateRouteTable(
	_ context.Context, associationID string,
) error {
	assoc, ok := m.rtAssocs.Get(associationID)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"route table association %q not found",
			associationID,
		)
	}

	// Real EC2 refuses to disassociate the main association; it is implicit in
	// the VPC rather than something the caller created.
	if assoc.Main {
		return errors.Newf(errors.InvalidArgument,
			"cannot disassociate the main route table association %q", associationID)
	}

	m.rtAssocs.Delete(associationID)

	return nil
}

func toRTAssocInfo(a *rtAssocData) driver.RouteTableAssociation {
	return driver.RouteTableAssociation{
		ID:           a.ID,
		RouteTableID: a.RouteTableID,
		SubnetID:     a.SubnetID,
		Main:         a.Main,
	}
}
