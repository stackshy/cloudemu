package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

type rtAssocData struct {
	ID           string
	RouteTableID string
	SubnetID     string
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
	if !m.rtAssocs.Delete(associationID) {
		return errors.Newf(
			errors.NotFound,
			"route table association %q not found",
			associationID,
		)
	}

	return nil
}

func toRTAssocInfo(a *rtAssocData) driver.RouteTableAssociation {
	return driver.RouteTableAssociation{
		ID:           a.ID,
		RouteTableID: a.RouteTableID,
		SubnetID:     a.SubnetID,
	}
}
