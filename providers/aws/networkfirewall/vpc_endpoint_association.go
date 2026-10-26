package networkfirewall

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateVpcEndpointAssociation(
	ctx context.Context, cfg nfdriver.CreateVPCEndpointAssociationConfig,
) (*nfdriver.VPCEndpointAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateARN("FirewallArn", cfg.FirewallARN); err != nil {
		return nil, err
	}

	if err := validateVPCID(cfg.VPCID); err != nil {
		return nil, err
	}

	if cfg.SubnetID == "" {
		return nil, invalid("SubnetMapping.SubnetId is required")
	}

	if err := validateIPType(cfg.IPAddressType); err != nil {
		return nil, err
	}

	if err := validateDescription(cfg.Description); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	fw, ok := m.firewallByARN(cfg.FirewallARN)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", cfg.FirewallARN)
	}

	id := idgen.GenerateID("")
	a := &nfdriver.VPCEndpointAssociation{
		ARN:              m.arn("vpc-endpoint-association", id),
		ID:               id,
		FirewallARN:      cfg.FirewallARN,
		VPCID:            cfg.VPCID,
		SubnetID:         cfg.SubnetID,
		IPAddressType:    cfg.IPAddressType,
		AvailabilityZone: m.associationZone(ctx, fw, cfg.SubnetID),
		Description:      cfg.Description,
		Status:           statusReady,
		Tags:             copyTags(cfg.Tags),
	}
	m.associations.Set(a.ARN, a)

	return cloneAssociation(a), nil
}

func (m *Mock) DescribeVpcEndpointAssociation(_ context.Context, arn string) (*nfdriver.VPCEndpointAssociation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, err := m.findAssociation(arn)
	if err != nil {
		return nil, err
	}

	return cloneAssociation(a), nil
}

func (m *Mock) DeleteVpcEndpointAssociation(_ context.Context, arn string) (*nfdriver.VPCEndpointAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, err := m.findAssociation(arn)
	if err != nil {
		return nil, err
	}

	out := cloneAssociation(a)
	out.Status = statusDeleting

	m.associations.Delete(a.ARN)

	return out, nil
}

func (m *Mock) ListVpcEndpointAssociations(
	_ context.Context, firewallARN string, opts nfdriver.ListOptions,
) ([]nfdriver.VPCEndpointAssociation, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if firewallARN != "" {
		if err := validateARN("FirewallArn", firewallARN); err != nil {
			return nil, "", err
		}
	}

	var matched []*nfdriver.VPCEndpointAssociation

	for _, a := range m.associations.SortedValues() {
		if firewallARN == "" || a.FirewallARN == firewallARN {
			matched = append(matched, a)
		}
	}

	items, next, err := paginate(matched, opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.VPCEndpointAssociation, 0, len(items))
	for _, a := range items {
		out = append(out, *cloneAssociation(a))
	}

	return out, next, nil
}

func (m *Mock) findAssociation(arn string) (*nfdriver.VPCEndpointAssociation, error) {
	if err := validateARN("VpcEndpointAssociationArn", arn); err != nil {
		return nil, err
	}

	a, ok := m.associations.Get(arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "VPC endpoint association %q not found", arn)
	}

	return a, nil
}

// associationCount counts the VPC endpoint associations of a firewall.
func (m *Mock) associationCount(firewallARN string) int {
	n := 0

	for _, a := range m.associations.SortedValues() {
		if a.FirewallARN == firewallARN {
			n++
		}
	}

	return n
}

func cloneAssociation(a *nfdriver.VPCEndpointAssociation) *nfdriver.VPCEndpointAssociation {
	out := *a
	out.Tags = copyTags(a.Tags)

	return &out
}

// associationZone is the Availability Zone of an association's subnet: the zone
// the firewall already assigned that subnet, else its real zone, else the zone
// its id maps to.
func (m *Mock) associationZone(ctx context.Context, fw *nfdriver.Firewall, subnetID string) string {
	if d, ok := fw.SubnetDetails[subnetID]; ok {
		return d.AvailabilityZone
	}

	if zone := m.resolveZone(ctx, subnetID); zone != "" {
		return zone
	}

	return zoneForKey(m.opts.Region, subnetID)
}
