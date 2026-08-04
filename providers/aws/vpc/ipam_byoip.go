package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ProvisionIpamByoasn provisions a bring-your-own ASN into an IPAM.
func (m *Mock) ProvisionIpamByoasn(_ context.Context, ipamID, asn string) (*driver.Byoasn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipams.Has(ipamID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", ipamID)
	}

	if asn == "" {
		return nil, errors.New(errors.InvalidArgument, "asn is required")
	}

	b := &driver.Byoasn{Asn: asn, IpamID: ipamID, State: "provisioned"}
	m.ipamByoasns.Set(asn, b)

	out := *b

	return &out, nil
}

// DeprovisionIpamByoasn removes a BYOASN.
func (m *Mock) DeprovisionIpamByoasn(_ context.Context, _, asn string) (*driver.Byoasn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.ipamByoasns.Get(asn)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "byoasn %q not found", asn)
	}

	b.State = ipamStateDeprovisioned

	m.ipamByoasns.Delete(asn)

	out := *b

	return &out, nil
}

// DescribeIpamByoasn returns all provisioned BYOASNs.
func (m *Mock) DescribeIpamByoasn(_ context.Context) ([]driver.Byoasn, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.ipamByoasns.SortedValues()
	out := make([]driver.Byoasn, 0, len(all))

	for _, b := range all {
		out = append(out, *b)
	}

	return out, nil
}

// AssociateIpamByoasn associates a BYOASN with a BYOIP CIDR.
func (m *Mock) AssociateIpamByoasn(_ context.Context, asn, cidr string) (*driver.AsnAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamByoasns.Has(asn) {
		return nil, errors.Newf(errors.InvalidArgument, "byoasn %q not found", asn)
	}

	bc, ok := m.ipamByoipCidrs.Get(cidr)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "byoip cidr %q not provisioned", cidr)
	}

	assoc := driver.AsnAssociation{Asn: asn, CIDR: cidr, State: "associated"}
	bc.AsnAssociations = append(bc.AsnAssociations, assoc)

	return &assoc, nil
}

// DisassociateIpamByoasn removes a BYOASN↔CIDR association.
func (m *Mock) DisassociateIpamByoasn(_ context.Context, asn, cidr string) (*driver.AsnAssociation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamByoasns.Has(asn) {
		return nil, errors.Newf(errors.InvalidArgument, "byoasn %q not found", asn)
	}

	assoc := driver.AsnAssociation{Asn: asn, CIDR: cidr, State: "disassociated"}

	if bc, ok := m.ipamByoipCidrs.Get(cidr); ok {
		kept := bc.AsnAssociations[:0:0]

		for _, a := range bc.AsnAssociations {
			if a.Asn != asn {
				kept = append(kept, a)
			}
		}

		bc.AsnAssociations = kept
	}

	return &assoc, nil
}

// ProvisionByoipCidr provisions a bring-your-own public IP CIDR.
func (m *Mock) ProvisionByoipCidr(_ context.Context, cidr, description string) (*driver.ByoipCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cidr == "" {
		return nil, errors.New(errors.InvalidArgument, "cidr is required")
	}

	bc := &driver.ByoipCidr{CIDR: cidr, Description: description, State: "provisioned"}
	m.ipamByoipCidrs.Set(cidr, bc)

	out := cloneByoipCidr(bc)

	return &out, nil
}

// DeprovisionByoipCidr removes a BYOIP CIDR.
func (m *Mock) DeprovisionByoipCidr(_ context.Context, cidr string) (*driver.ByoipCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bc, ok := m.ipamByoipCidrs.Get(cidr)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "byoip cidr %q not found", cidr)
	}

	if bc.AdvertisementType == "advertised" {
		return nil, errors.Newf(errors.FailedPrecondition, "byoip cidr %q is advertised", cidr)
	}

	bc.State = ipamStateDeprovisioned

	m.ipamByoipCidrs.Delete(cidr)

	out := cloneByoipCidr(bc)

	return &out, nil
}

// MoveByoipCidrToIpam moves a BYOIP CIDR under IPAM management.
func (m *Mock) MoveByoipCidrToIpam(_ context.Context, cidr, ipamPoolID string) (*driver.ByoipCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamPools.Has(ipamPoolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", ipamPoolID)
	}

	bc, ok := m.ipamByoipCidrs.Get(cidr)
	if !ok {
		bc = &driver.ByoipCidr{CIDR: cidr, State: "provisioned"}
		m.ipamByoipCidrs.Set(cidr, bc)
	}

	out := cloneByoipCidr(bc)

	return &out, nil
}

// DescribeByoipCidrs returns all BYOIP CIDRs.
func (m *Mock) DescribeByoipCidrs(_ context.Context) ([]driver.ByoipCidr, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.ipamByoipCidrs.SortedValues()
	out := make([]driver.ByoipCidr, 0, len(all))

	for _, bc := range all {
		out = append(out, cloneByoipCidr(bc))
	}

	return out, nil
}

// AdvertiseByoipCidr advertises a BYOIP CIDR to the internet.
func (m *Mock) AdvertiseByoipCidr(_ context.Context, cidr string) (*driver.ByoipCidr, error) {
	return m.setByoipAdvertisement(cidr, "advertised")
}

// WithdrawByoipCidr withdraws a BYOIP CIDR advertisement.
func (m *Mock) WithdrawByoipCidr(_ context.Context, cidr string) (*driver.ByoipCidr, error) {
	return m.setByoipAdvertisement(cidr, "withdrawn")
}

func (m *Mock) setByoipAdvertisement(cidr, advertisement string) (*driver.ByoipCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bc, ok := m.ipamByoipCidrs.Get(cidr)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "byoip cidr %q not found", cidr)
	}

	bc.AdvertisementType = advertisement

	out := cloneByoipCidr(bc)

	return &out, nil
}

func cloneByoipCidr(bc *driver.ByoipCidr) driver.ByoipCidr {
	out := *bc
	out.AsnAssociations = append([]driver.AsnAssociation(nil), bc.AsnAssociations...)

	return out
}
