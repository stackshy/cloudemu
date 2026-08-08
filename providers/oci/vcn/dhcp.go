package vcn

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// DHCP option server types. VcnLocalPlusInternet is the resolver OCI gives a
// new VCN: the VCN's own records first, then the internet.
const (
	ServerTypeVCNLocalPlusInternet = "VcnLocalPlusInternet"
	ServerTypeCustomDNS            = "CustomDnsServer"
)

// DHCPOptions is OCI's DHCP options set, attached to a VCN and referenced by
// its subnets. The portable networking driver has no equivalent, so this is
// an OCI-only capability discovered by type assertion.
type DHCPOptions struct {
	ID              string
	VCNID           string
	Name            string
	ServerType      string
	CustomDNSServer []string
	SearchDomains   []string
	IsDefault       bool
	State           string
}

type dhcpOptionsData struct {
	ID              string
	VCNID           string
	Name            string
	ServerType      string
	CustomDNSServer []string
	SearchDomains   []string
	IsDefault       bool
}

// CreateDHCPOptions creates a DHCP options set in a VCN.
func (m *Mock) CreateDHCPOptions(
	_ context.Context, vcnID, name, serverType string, customDNS, searchDomains []string,
) (*DHCPOptions, error) {
	if vcnID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "VCN OCID is required")
	}

	if !m.vcns.Has(vcnID) {
		return nil, cerrors.Newf(cerrors.NotFound, "VCN %q not found", vcnID)
	}

	if serverType == ServerTypeCustomDNS && len(customDNS) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "custom DNS servers are required for CustomDnsServer")
	}

	return m.addDHCPOptions(vcnID, name, serverType, customDNS, searchDomains, false), nil
}

// newDefaultDHCPOptions creates the DHCP options OCI attaches to a new VCN.
func (m *Mock) newDefaultDHCPOptions(v *vcnData) string {
	return m.addDHCPOptions(v.ID, "Default DHCP Options", ServerTypeVCNLocalPlusInternet, nil, nil, true).ID
}

// addDHCPOptions stores a DHCP options set.
func (m *Mock) addDHCPOptions(
	vcnID, name, serverType string, customDNS, searchDomains []string, isDefault bool,
) *DHCPOptions {
	if serverType == "" {
		serverType = ServerTypeVCNLocalPlusInternet
	}

	id := m.newOCID(typeDHCPOptions)
	d := &dhcpOptionsData{
		ID:              id,
		VCNID:           vcnID,
		Name:            name,
		ServerType:      serverType,
		CustomDNSServer: copyStringSlice(customDNS),
		SearchDomains:   copyStringSlice(searchDomains),
		IsDefault:       isDefault,
	}

	m.dhcpOptions.Set(id, d)
	m.record(id)

	info := toDHCPOptionsInfo(d)

	return &info
}

// DeleteDHCPOptions deletes a DHCP options set. The VCN's default set only
// goes away with the VCN.
func (m *Mock) DeleteDHCPOptions(_ context.Context, id string) error {
	d, ok := m.dhcpOptions.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DHCP options %q not found", id)
	}

	if d.IsDefault {
		return cerrors.Newf(cerrors.FailedPrecondition, "cannot delete default DHCP options %q", id)
	}

	m.dhcpOptions.Delete(id)
	m.forget(id)

	return nil
}

// DescribeDHCPOptions returns DHCP options sets matching the given OCIDs, or
// all if empty.
func (m *Mock) DescribeDHCPOptions(_ context.Context, ids []string) ([]DHCPOptions, error) {
	return describeResources(m.dhcpOptions, ids, toDHCPOptionsInfo), nil
}

// UpdateDHCPOptions changes a DHCP options set. A nil pointer or slice leaves
// that field alone.
func (m *Mock) UpdateDHCPOptions(
	_ context.Context, id string, name *string, serverType string, customDNS, searchDomains []string,
) (*DHCPOptions, error) {
	d, ok := m.dhcpOptions.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "DHCP options %q not found", id)
	}

	if name != nil {
		d.Name = *name
	}

	if serverType != "" {
		d.ServerType = serverType
	}

	if customDNS != nil {
		d.CustomDNSServer = copyStringSlice(customDNS)
	}

	if searchDomains != nil {
		d.SearchDomains = copyStringSlice(searchDomains)
	}

	out := toDHCPOptionsInfo(d)

	return &out, nil
}

func toDHCPOptionsInfo(d *dhcpOptionsData) DHCPOptions {
	return DHCPOptions{
		ID:              d.ID,
		VCNID:           d.VCNID,
		Name:            d.Name,
		ServerType:      d.ServerType,
		CustomDNSServer: copyStringSlice(d.CustomDNSServer),
		SearchDomains:   copyStringSlice(d.SearchDomains),
		IsDefault:       d.IsDefault,
		State:           StateAvailable,
	}
}
