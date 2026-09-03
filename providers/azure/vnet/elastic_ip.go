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

// UpdateAzurePublicIP overwrites the mutable fields of an existing public IP in
// place (keyed by its allocation id), applying the same SKU/allocation-method
// defaults as AllocateAddress and recomputing the DNS FQDN, so a repeat ARM
// CreateOrUpdate PUT mutates the resource instead of minting a duplicate. The
// allocation id, address and any existing association are preserved.
//
//nolint:gocritic // hugeParam: cfg mirrors AllocateAddress's driver signature.
func (m *Mock) UpdateAzurePublicIP(_ context.Context, allocationID string, cfg driver.ElasticIPConfig) error {
	sku := cfg.SKU
	if sku == "" {
		sku = "Standard"
	}

	allocMethod := cfg.AllocationMethod
	if allocMethod == "" {
		allocMethod = "Static"
	}

	found := m.eips.Update(allocationID, func(e *eipData) *eipData {
		cp := *e
		cp.Tags = copyTags(cfg.Tags)
		cp.SKU = sku
		cp.AllocationMethod = allocMethod
		cp.IdleTimeoutMinutes = cfg.IdleTimeoutMinutes
		cp.DNSDomainNameLabel = cfg.DNSDomainNameLabel

		cp.Zones = append([]string(nil), cfg.Zones...)

		if cfg.DNSDomainNameLabel != "" {
			cp.DNSFQDN = cfg.DNSDomainNameLabel + "." + defaultFQDNRegion + ".cloudapp.azure.com"
		} else {
			cp.DNSFQDN = ""
		}

		return &cp
	})
	if !found {
		return cerrors.Newf(cerrors.NotFound, "public IP %q not found", allocationID)
	}

	return nil
}

// clearEIPAssociation returns a copy of e with its association and instance
// binding cleared, so a public IP is freed copy-on-write instead of mutating
// the shared pointer a reader may still hold. Clearing InstanceID as well as
// AssociationID is safe for NAT-gateway bindings, which never set InstanceID.
func clearEIPAssociation(e *eipData) *eipData {
	cp := *e
	cp.AssociationID = ""
	cp.InstanceID = ""

	return &cp
}

// ReleaseAddress releases a public IP address. The still-associated guard and
// the delete run in one locked span via UpdateOrDelete, so the address cannot
// be associated between the check and the delete (no check-then-act race).
func (m *Mock) ReleaseAddress(
	_ context.Context, allocationID string,
) error {
	var associated bool

	found := m.eips.UpdateOrDelete(allocationID, func(e *eipData) (*eipData, bool) {
		if e.AssociationID != "" {
			associated = true
			return e, true // keep
		}

		return nil, false // delete
	})
	if !found {
		return cerrors.Newf(
			cerrors.NotFound,
			"public IP %q not found", allocationID,
		)
	}

	if associated {
		return cerrors.Newf(
			cerrors.FailedPrecondition,
			"public IP %q is still associated", allocationID,
		)
	}

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
	var (
		conflict error
		assocID  string
	)

	found := m.eips.Update(allocationID, func(e *eipData) *eipData {
		if e.AssociationID != "" {
			conflict = cerrors.Newf(
				cerrors.FailedPrecondition,
				"public IP %q is already associated", allocationID,
			)

			return e
		}

		assocID = idgen.GenerateID("ipassoc-")
		cp := *e
		cp.AssociationID = assocID
		cp.InstanceID = in.InstanceID

		return &cp
	})
	if !found {
		return "", cerrors.Newf(
			cerrors.NotFound,
			"public IP %q not found", allocationID,
		)
	}

	if conflict != nil {
		return "", conflict
	}

	return assocID, nil
}

// DisassociateAddress removes a public IP association. The matching address is
// freed copy-on-write, and the association id is re-checked under the store
// lock so a concurrent release/re-allocation cannot be cleared by mistake.
func (m *Mock) DisassociateAddress(
	_ context.Context, associationID string,
) error {
	for id, eip := range m.eips.All() {
		if eip.AssociationID != associationID {
			continue
		}

		m.eips.Update(id, func(e *eipData) *eipData {
			if e.AssociationID != associationID {
				return e
			}

			return clearEIPAssociation(e)
		})

		return nil
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
