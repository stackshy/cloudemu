package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
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

// DeleteInternetGateway deletes the internet gateway.
func (m *Mock) DeleteInternetGateway(
	_ context.Context, id string,
) error {
	igw, ok := m.igws.Get(id)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"internet gateway %q not found", id,
		)
	}

	if igw.State == IGWStateAttached {
		return errors.Newf(
			errors.FailedPrecondition,
			"internet gateway %q is still attached", id,
		)
	}

	m.igws.Delete(id)

	return nil
}

// DescribeInternetGateways returns internet gateways
// matching the given IDs, or all if ids is empty.
func (m *Mock) DescribeInternetGateways(
	_ context.Context, ids []string,
) ([]driver.InternetGateway, error) {
	return describeResources(m.igws, ids, toIGWInfo), nil
}

// AttachInternetGateway attaches an internet gateway to a VPC.
func (m *Mock) AttachInternetGateway(
	_ context.Context, igwID, vpcID string,
) error {
	igw, ok := m.igws.Get(igwID)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"internet gateway %q not found", igwID,
		)
	}

	if igw.State == IGWStateAttached {
		return errors.Newf(
			errors.FailedPrecondition,
			"internet gateway %q is already attached", igwID,
		)
	}

	if !m.vpcs.Has(vpcID) {
		return errors.Newf(
			errors.NotFound, "vpc %q not found", vpcID,
		)
	}

	igw.VpcID = vpcID
	igw.State = IGWStateAttached

	return nil
}

// DetachInternetGateway detaches an internet gateway from a VPC.
func (m *Mock) DetachInternetGateway(
	_ context.Context, igwID, vpcID string,
) error {
	igw, ok := m.igws.Get(igwID)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"internet gateway %q not found", igwID,
		)
	}

	if igw.State != IGWStateAttached || igw.VpcID != vpcID {
		return errors.Newf(
			errors.FailedPrecondition,
			"internet gateway %q is not attached to vpc %q",
			igwID, vpcID,
		)
	}

	igw.VpcID = ""
	igw.State = IGWStateDetached

	return nil
}

func toIGWInfo(igw *igwData) driver.InternetGateway {
	return driver.InternetGateway{
		ID:    igw.ID,
		VpcID: igw.VpcID,
		State: igw.State,
		Tags:  copyTags(igw.Tags),
	}
}
