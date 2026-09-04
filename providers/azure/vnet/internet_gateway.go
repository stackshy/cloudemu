package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Internet gateway state constants.
const (
	IGWStateDetached = "detached"
	IGWStateAttached = "attached"
)

type igwData struct {
	ID    string
	VpcID string
	State string
	Tags  map[string]string
}

// CreateInternetGateway creates a new internet gateway.
func (m *Mock) CreateInternetGateway(
	_ context.Context, cfg driver.InternetGatewayConfig,
) (*driver.InternetGateway, error) {
	id := idgen.GenerateID("igw-")

	igw := &igwData{
		ID:    id,
		State: IGWStateDetached,
		Tags:  copyTags(cfg.Tags),
	}
	m.igws.Set(id, igw)

	info := toIGWInfo(igw)

	return &info, nil
}

// DeleteInternetGateway deletes the internet gateway. The still-attached guard
// and the delete run in one locked span via UpdateOrDelete, so the gateway
// cannot be attached between the check and the delete (no check-then-act race).
func (m *Mock) DeleteInternetGateway(
	_ context.Context, id string,
) error {
	var attached bool

	found := m.igws.UpdateOrDelete(id, func(igw *igwData) (*igwData, bool) {
		if igw.State == IGWStateAttached {
			attached = true
			return igw, true // keep
		}

		return nil, false // delete
	})
	if !found {
		return cerrors.Newf(
			cerrors.NotFound,
			"internet gateway %q not found", id,
		)
	}

	if attached {
		return cerrors.Newf(
			cerrors.FailedPrecondition,
			"internet gateway %q is still attached", id,
		)
	}

	return nil
}

// DescribeInternetGateways returns internet gateways
// matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeInternetGateways(
	_ context.Context, ids []string,
) ([]driver.InternetGateway, error) {
	return describeResources(m.igws, ids, toIGWInfo), nil
}

// AttachInternetGateway attaches an internet gateway
// to a virtual network.
func (m *Mock) AttachInternetGateway(
	_ context.Context, igwID, vpcID string,
) error {
	var opErr error

	found := m.igws.Update(igwID, func(igw *igwData) *igwData {
		if igw.State == IGWStateAttached {
			opErr = cerrors.Newf(
				cerrors.FailedPrecondition,
				"internet gateway %q is already attached", igwID,
			)

			return igw
		}

		if !m.vpcs.Has(vpcID) {
			opErr = cerrors.Newf(
				cerrors.NotFound, "vnet %q not found", vpcID,
			)

			return igw
		}

		cp := *igw
		cp.VpcID = vpcID
		cp.State = IGWStateAttached

		return &cp
	})
	if !found {
		return cerrors.Newf(
			cerrors.NotFound,
			"internet gateway %q not found", igwID,
		)
	}

	return opErr
}

// DetachInternetGateway detaches an internet gateway
// from a virtual network.
func (m *Mock) DetachInternetGateway(
	_ context.Context, igwID, vpcID string,
) error {
	var opErr error

	found := m.igws.Update(igwID, func(igw *igwData) *igwData {
		if igw.State != IGWStateAttached || igw.VpcID != vpcID {
			opErr = cerrors.Newf(
				cerrors.FailedPrecondition,
				"internet gateway %q is not attached to vnet %q",
				igwID, vpcID,
			)

			return igw
		}

		cp := *igw
		cp.VpcID = ""
		cp.State = IGWStateDetached

		return &cp
	})
	if !found {
		return cerrors.Newf(
			cerrors.NotFound,
			"internet gateway %q not found", igwID,
		)
	}

	return opErr
}

func toIGWInfo(igw *igwData) driver.InternetGateway {
	return driver.InternetGateway{
		ID:    igw.ID,
		VpcID: igw.VpcID,
		State: igw.State,
		Tags:  copyTags(igw.Tags),
	}
}
