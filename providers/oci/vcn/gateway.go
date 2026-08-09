package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type igwData struct {
	ID    string
	VCNID string
	State string
	Tags  map[string]string
}

type natGatewayData struct {
	ID        string
	SubnetID  string
	VCNID     string
	PublicIP  string
	State     string
	CreatedAt string
	Tags      map[string]string
}

// CreateInternetGateway creates an internet gateway. OCI attaches it to a VCN
// in the same call; the portable driver splits creation from attachment, so a
// gateway starts detached.
func (m *Mock) CreateInternetGateway(_ context.Context, cfg driver.InternetGatewayConfig) (*driver.InternetGateway, error) {
	id := m.newOCID(typeInternetGW)
	igw := &igwData{
		ID:    id,
		State: StateDetached,
		Tags:  copyTags(cfg.Tags),
	}

	m.igws.Set(id, igw)
	m.record(id)

	info := toIGWInfo(igw)

	return &info, nil
}

// DeleteInternetGateway deletes an internet gateway once it is detached.
func (m *Mock) DeleteInternetGateway(_ context.Context, id string) error {
	igw, ok := m.igws.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "internet gateway %q not found", id)
	}

	if igw.State == StateAttached {
		return cerrors.Newf(cerrors.FailedPrecondition, "internet gateway %q is still attached", id)
	}

	m.igws.Delete(id)
	m.forget(id)

	return nil
}

// DescribeInternetGateways returns internet gateways matching the given
// OCIDs, or all if empty.
func (m *Mock) DescribeInternetGateways(_ context.Context, ids []string) ([]driver.InternetGateway, error) {
	return describeResources(m.igws, ids, toIGWInfo), nil
}

// AttachInternetGateway attaches an internet gateway to a VCN.
func (m *Mock) AttachInternetGateway(_ context.Context, igwID, vpcID string) error {
	return mutate(m.igws, igwID, igwNotFound(igwID), func(igw *igwData) error {
		if igw.State == StateAttached {
			return cerrors.Newf(cerrors.FailedPrecondition, "internet gateway %q is already attached", igwID)
		}

		if !m.vcns.Has(vpcID) {
			return cerrors.Newf(cerrors.NotFound, "VCN %q not found", vpcID)
		}

		igw.VCNID = vpcID
		igw.State = StateAttached

		return nil
	})
}

// DetachInternetGateway detaches an internet gateway from its VCN.
func (m *Mock) DetachInternetGateway(_ context.Context, igwID, vpcID string) error {
	return mutate(m.igws, igwID, igwNotFound(igwID), func(igw *igwData) error {
		if igw.State != StateAttached || igw.VCNID != vpcID {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"internet gateway %q is not attached to VCN %q", igwID, vpcID)
		}

		igw.VCNID = ""
		igw.State = StateDetached

		return nil
	})
}

func igwNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "internet gateway %q not found", id)
}

// CreateNATGateway creates a NAT gateway. OCI hangs a NAT gateway off the VCN
// rather than a subnet, so cfg.SubnetID may name either and the VCN is
// resolved from whichever it is.
func (m *Mock) CreateNATGateway(_ context.Context, cfg driver.NATGatewayConfig) (*driver.NATGateway, error) {
	if cfg.SubnetID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN or subnet OCID is required")
	}

	vcnID, subnetID, err := m.resolveVCN(cfg.SubnetID)
	if err != nil {
		return nil, err
	}

	id := m.newOCID(typeNATGateway)
	nat := &natGatewayData{
		ID:        id,
		SubnetID:  subnetID,
		VCNID:     vcnID,
		PublicIP:  mockPublicIP(id),
		State:     StateAvailable,
		CreatedAt: m.now(),
		Tags:      copyTags(cfg.Tags),
	}

	m.natGateways.Set(id, nat)
	m.record(id)

	info := toNATGatewayInfo(nat)

	return &info, nil
}

// resolveVCN accepts a VCN or subnet OCID and returns the VCN it belongs to
// along with the subnet, if one was named.
func (m *Mock) resolveVCN(id string) (vcnID, subnetID string, err error) {
	if m.vcns.Has(id) {
		return id, "", nil
	}

	if s, ok := m.subnets.Get(id); ok {
		return s.VCNID, s.ID, nil
	}

	return "", "", cerrors.Newf(cerrors.NotFound, "VCN or subnet %q not found", id)
}

// DeleteNATGateway deletes a NAT gateway.
func (m *Mock) DeleteNATGateway(_ context.Context, id string) error {
	if !m.natGateways.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "NAT gateway %q not found", id)
	}

	m.forget(id)

	return nil
}

// DescribeNATGateways returns NAT gateways matching the given OCIDs, or all
// if empty.
func (m *Mock) DescribeNATGateways(_ context.Context, ids []string) ([]driver.NATGateway, error) {
	return describeResources(m.natGateways, ids, toNATGatewayInfo), nil
}

func toIGWInfo(igw *igwData) driver.InternetGateway {
	return driver.InternetGateway{
		ID:    igw.ID,
		VpcID: igw.VCNID,
		State: igw.State,
		Tags:  copyTags(igw.Tags),
	}
}

func toNATGatewayInfo(n *natGatewayData) driver.NATGateway {
	return driver.NATGateway{
		ID:        n.ID,
		SubnetID:  n.SubnetID,
		VPCID:     n.VCNID,
		PublicIP:  n.PublicIP,
		State:     n.State,
		CreatedAt: n.CreatedAt,
		Tags:      copyTags(n.Tags),
	}
}
