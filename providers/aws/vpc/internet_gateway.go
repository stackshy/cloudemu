package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range ids {
		if !m.igws.Has(id) {
			return nil, errors.Newf(errors.NotFound, "internet gateway %q not found", id)
		}
	}

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

	// An internet gateway attaches to exactly one VPC; re-attaching an
	// already-attached gateway is Resource.AlreadyAssociated, not a dependency
	// violation.
	if igw.State == IGWStateAttached {
		return errors.Newf(
			errors.AlreadyExists,
			"internet gateway %q is already attached to vpc %q", igwID, igw.VpcID,
		)
	}

	if !m.vpcs.Has(vpcID) {
		return errors.Newf(
			errors.NotFound, "vpc %q not found", vpcID,
		)
	}

	// One internet gateway per VPC: reject if the target VPC already has one
	// attached. Real EC2 answers Resource.AlreadyAssociated ("Network vpc-…
	// already has an internet gateway attached").
	if existing := m.vpcAttachedIGW(vpcID); existing != "" {
		return errors.Newf(
			errors.AlreadyExists,
			"vpc %q already has internet gateway %q attached", vpcID, existing,
		)
	}

	igw.VpcID = vpcID
	igw.State = IGWStateAttached

	return nil
}

// vpcAttachedIGW returns the id of the internet gateway already attached to the
// VPC, or "" if none is. Enforces the one-IGW-per-VPC invariant.
func (m *Mock) vpcAttachedIGW(vpcID string) string {
	for _, igw := range m.igws.All() {
		if igw.State == IGWStateAttached && igw.VpcID == vpcID {
			return igw.ID
		}
	}

	return ""
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
