package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type eipData struct {
	AllocationID       string
	PublicIP           string
	AssociationID      string
	InstanceID         string
	Tags               map[string]string
	SKU                string
	AllocationMethod   string
	Zones              []string
	IdleTimeoutMinutes int
	DNSDomainNameLabel string
	DNSFQDN            string
}

// defaultFQDNRegion is the region segment used to build a mock DNS FQDN for a
// public IP's domainNameLabel. Real Azure computes this from the resource's
// actual location plus a per-cloud hash segment; a fixed region keeps the mock
// deterministic without threading location through ElasticIPConfig.
const defaultFQDNRegion = "eastus"

// AllocateAddress allocates a new public IP address.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the Networking driver interface.
func (m *Mock) AllocateAddress(
	_ context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	allocID := idgen.GenerateID("ipalloc-")

	// Real Azure defaults a public IP to the Standard SKU with Static allocation
	// when the request omits them.
	sku := cfg.SKU
	if sku == "" {
		sku = "Standard"
	}

	allocMethod := cfg.AllocationMethod
	if allocMethod == "" {
		allocMethod = "Static"
	}

	eip := &eipData{
		AllocationID:       allocID,
		PublicIP:           mockPublicIP(allocID),
		Tags:               copyTags(cfg.Tags),
		SKU:                sku,
		AllocationMethod:   allocMethod,
		Zones:              append([]string(nil), cfg.Zones...),
		IdleTimeoutMinutes: cfg.IdleTimeoutMinutes,
		DNSDomainNameLabel: cfg.DNSDomainNameLabel,
	}

	if cfg.DNSDomainNameLabel != "" {
		eip.DNSFQDN = cfg.DNSDomainNameLabel + "." + defaultFQDNRegion + ".cloudapp.azure.com"
	}

	m.eips.Set(allocID, eip)

	info := toEIPInfo(eip)

	return &info, nil
}

// ReleaseAddress releases a public IP address.
func (m *Mock) ReleaseAddress(
	_ context.Context, allocationID string,
) error {
	eip, ok := m.eips.Get(allocationID)
	if !ok {
		return cerrors.Newf(
			cerrors.NotFound,
			"public IP %q not found", allocationID,
		)
	}

	if eip.AssociationID != "" {
		return cerrors.Newf(
			cerrors.FailedPrecondition,
			"public IP %q is still associated", allocationID,
		)
	}

	m.eips.Delete(allocationID)

	return nil
}

// DescribeAddresses returns public IPs matching the given
// allocation IDs, or all if ids is empty.
func (m *Mock) DescribeAddresses(
	_ context.Context, ids []string,
) ([]driver.ElasticIP, error) {
	return describeResources(m.eips, ids, toEIPInfo), nil
}

// AssociateAddress associates a public IP with an instance.
func (m *Mock) AssociateAddress(
	_ context.Context, allocationID string, in driver.AssociateAddressInput,
) (string, error) {
	eip, ok := m.eips.Get(allocationID)
	if !ok {
		return "", cerrors.Newf(
			cerrors.NotFound,
			"public IP %q not found", allocationID,
		)
	}

	if eip.AssociationID != "" {
		return "", cerrors.Newf(
			cerrors.FailedPrecondition,
			"public IP %q is already associated", allocationID,
		)
	}

	assocID := idgen.GenerateID("ipassoc-")
	eip.AssociationID = assocID
	eip.InstanceID = in.InstanceID

	return assocID, nil
}

// DisassociateAddress removes a public IP association.
func (m *Mock) DisassociateAddress(
	_ context.Context, associationID string,
) error {
	for _, eip := range m.eips.All() {
		if eip.AssociationID == associationID {
			eip.AssociationID = ""
			eip.InstanceID = ""

			return nil
		}
	}

	return cerrors.Newf(
		cerrors.NotFound,
		"association %q not found", associationID,
	)
}

func toEIPInfo(eip *eipData) driver.ElasticIP {
	return driver.ElasticIP{
		AllocationID:       eip.AllocationID,
		PublicIP:           eip.PublicIP,
		AssociationID:      eip.AssociationID,
		InstanceID:         eip.InstanceID,
		Tags:               copyTags(eip.Tags),
		SKU:                eip.SKU,
		AllocationMethod:   eip.AllocationMethod,
		Zones:              append([]string(nil), eip.Zones...),
		IdleTimeoutMinutes: eip.IdleTimeoutMinutes,
		DNSDomainNameLabel: eip.DNSDomainNameLabel,
		DNSFQDN:            eip.DNSFQDN,
	}
}
