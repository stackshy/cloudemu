package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// NAT gateway state constants.
const (
	NATStateAvailable = "available"
)

type natGatewayData struct {
	ID        string
	SubnetID  string
	VPCID     string
	PublicIP  string
	State     string
	CreatedAt string
	Tags      map[string]string
	// AllocationID is the Elastic IP allocation bound to this NAT gateway, set
	// when the caller passes NATGatewayConfig.AllocationID. Unlike AWS, an
	// Azure NAT gateway is not bound to a subnet at creation time (subnets
	// attach to it afterwards via their own natGateway reference), so SubnetID
	// stays empty for the Azure wire handler's normal flow.
	AllocationID string
}

// CreateNATGateway creates a NAT gateway, optionally binding it to a subnet
// (AWS-style, if a caller passes one) and/or a public IP allocation (Azure's
// natGateways.properties.publicIpAddresses). Binding an already-associated
// allocation is rejected, matching a public IP that can only serve one
// resource at a time.
func (m *Mock) CreateNATGateway(_ context.Context, cfg driver.NATGatewayConfig) (*driver.NATGateway, error) {
	var vpcID string

	if cfg.SubnetID != "" {
		subnet, ok := m.subnets.Get(cfg.SubnetID)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "subnet %q not found", cfg.SubnetID)
		}

		vpcID = subnet.VPCID
	}

	id := idgen.GenerateID("natgw-")

	nat := &natGatewayData{
		ID:        id,
		SubnetID:  cfg.SubnetID,
		VPCID:     vpcID,
		State:     NATStateAvailable,
		CreatedAt: m.opts.Clock.Now().Format(timeFormat),
		Tags:      copyTags(cfg.Tags),
	}

	if cfg.AllocationID != "" {
		publicIP, err := m.bindNATGatewayAllocation(cfg.AllocationID)
		if err != nil {
			return nil, err
		}

		nat.AllocationID = cfg.AllocationID
		nat.PublicIP = publicIP
	}

	m.natGateways.Set(id, nat)

	info := toNATGatewayInfo(nat)

	return &info, nil
}

// bindNATGatewayAllocation atomically marks the given Elastic IP allocation as
// associated (rejecting one already in use) and returns its address, so the
// check-and-set can't race a concurrent AssociateAddress/CreateNATGateway call.
func (m *Mock) bindNATGatewayAllocation(allocationID string) (string, error) {
	var (
		conflict error
		address  string
	)

	found := m.eips.Update(allocationID, func(e *eipData) *eipData {
		if e.AssociationID != "" {
			conflict = cerrors.Newf(cerrors.FailedPrecondition,
				"public IP %q is already associated", allocationID)

			return e
		}

		cp := *e
		cp.AssociationID = idgen.GenerateID("natgwassoc-")
		address = cp.PublicIP

		return &cp
	})

	if !found {
		return "", cerrors.Newf(cerrors.NotFound, "public IP %q not found", allocationID)
	}

	if conflict != nil {
		return "", conflict
	}

	return address, nil
}

// DeleteNATGateway deletes the NAT gateway with the given ID, freeing any
// bound Elastic IP allocation so it can be released or reused.
func (m *Mock) DeleteNATGateway(_ context.Context, id string) error {
	var allocationID string

	// Read the bound allocation and delete the gateway in one locked span so the
	// two cannot be split by a concurrent update.
	found := m.natGateways.UpdateOrDelete(id, func(n *natGatewayData) (*natGatewayData, bool) {
		allocationID = n.AllocationID
		return nil, false // delete
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "NAT gateway %q not found", id)
	}

	if allocationID != "" {
		m.eips.Update(allocationID, clearEIPAssociation)
	}

	return nil
}

// UpdateAzureNATGateway re-applies the mutable fields of an existing NAT gateway
// (its bound public-IP allocation and tags) in place, so a repeat ARM
// CreateOrUpdate PUT re-associates the public IP and reflects tag changes rather
// than discarding them. When the requested allocation differs from the current
// one it binds the new public IP first (rejecting one already in use) and only
// then frees the previous binding, so a failed rebind leaves the gateway
// untouched. An empty allocationID detaches any bound public IP.
func (m *Mock) UpdateAzureNATGateway(_ context.Context, id, allocationID string, tags map[string]string) error {
	nat, ok := m.natGateways.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "NAT gateway %q not found", id)
	}

	newPublicIP := nat.PublicIP

	if allocationID != nat.AllocationID {
		newPublicIP = ""

		if allocationID != "" {
			address, err := m.bindNATGatewayAllocation(allocationID)
			if err != nil {
				return err
			}

			newPublicIP = address
		}

		if nat.AllocationID != "" {
			m.eips.Update(nat.AllocationID, clearEIPAssociation)
		}
	}

	// Single copy-on-write swap applies the (possibly unchanged) allocation and
	// the new tags onto a fresh *natGatewayData, never mutating the shared one.
	m.natGateways.Update(id, func(n *natGatewayData) *natGatewayData {
		cp := *n
		cp.AllocationID = allocationID
		cp.PublicIP = newPublicIP
		cp.Tags = copyTags(tags)

		return &cp
	})

	return nil
}

// DescribeNATGateways returns NAT gateways matching the given IDs, or all if empty.
func (m *Mock) DescribeNATGateways(_ context.Context, ids []string) ([]driver.NATGateway, error) {
	return describeResources(m.natGateways, ids, toNATGatewayInfo), nil
}

func toNATGatewayInfo(n *natGatewayData) driver.NATGateway {
	return driver.NATGateway{
		ID:           n.ID,
		SubnetID:     n.SubnetID,
		VPCID:        n.VPCID,
		PublicIP:     n.PublicIP,
		State:        n.State,
		CreatedAt:    n.CreatedAt,
		Tags:         copyTags(n.Tags),
		AllocationID: n.AllocationID,
	}
}
