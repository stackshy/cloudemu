package vnet

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

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
	SKUTier            string
	IPVersion          string
	AllocationMethod   string
	Zones              []string
	IdleTimeoutMinutes int
	DNSDomainNameLabel string
	DNSFQDN            string
	// ResourceGUID is the Azure-only persisted identifier (ARM
	// properties.resourceGuid), assigned once in AllocateAddress and preserved
	// across every UpdateAzurePublicIP, matching how network interfaces
	// preserve theirs.
	ResourceGUID string
	// Location is the region the public IP was created in (ARM location).
	Location string
}

// defaultPublicIPLocation is the region assumed when a public IP is created
// without one (the portable API never sets Location).
const defaultPublicIPLocation = "eastus"

// publicIPFQDN builds the DNS name real Azure assigns a public IP with a
// domainNameLabel: <label>.<region>.cloudapp.azure.com, where region is the
// resource's canonical location (lower-case, no spaces). An empty label has
// no FQDN.
func publicIPFQDN(label, location string) string {
	if label == "" {
		return ""
	}

	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))
	if region == "" {
		region = defaultPublicIPLocation
	}

	return label + "." + region + ".cloudapp.azure.com"
}

// Azure public-IP defaults applied when a request omits the field, matching the
// values a real publicIPAddresses GET reports: Standard SKU, Regional tier,
// Static allocation, IPv4 address family and a 4-minute TCP idle timeout.
const (
	defaultPublicIPSKU       = "Standard"
	defaultPublicIPSKUTier   = "Regional"
	defaultPublicIPAllocMeth = "Static"
	defaultPublicIPVersion   = "IPv4"
	defaultIdleTimeoutMin    = 4
)

// applyPublicIPDefaults fills the Azure public-IP fields ARM defaults when the
// request omits them, so both AllocateAddress and UpdateAzurePublicIP surface
// the same GET-visible values (sku.name, sku.tier, publicIPAllocationMethod,
// publicIPAddressVersion, idleTimeoutInMinutes) that real Azure reports.
//
//nolint:gocritic // hugeParam: cfg mirrors AllocateAddress's driver signature.
func applyPublicIPDefaults(cfg driver.ElasticIPConfig) driver.ElasticIPConfig {
	if cfg.SKU == "" {
		cfg.SKU = defaultPublicIPSKU
	}

	if cfg.SKUTier == "" {
		cfg.SKUTier = defaultPublicIPSKUTier
	}

	if cfg.AllocationMethod == "" {
		cfg.AllocationMethod = defaultPublicIPAllocMeth
	}

	if cfg.IPVersion == "" {
		cfg.IPVersion = defaultPublicIPVersion
	}

	if cfg.IdleTimeoutMinutes == 0 {
		cfg.IdleTimeoutMinutes = defaultIdleTimeoutMin
	}

	if cfg.Location == "" {
		cfg.Location = defaultPublicIPLocation
	}

	return cfg
}

// publicIPFirstOctet is the leading octet of every emulated public IP. 20.0.0.0/8
// is one of the ranges Microsoft announces for Azure public IPs, so addresses
// look like real ones instead of the RFC 1918 space a public IP never uses.
const publicIPFirstOctet = 20

// publicIPHostOctets is how many values the last octet may take (1..254).
const publicIPHostOctets = 254

// publicAddress derives a public-looking IPv4 address for a new allocation from
// a hash of its id, salting and rehashing on the rare collision with an address
// already handed out. The result is stored on the allocation, so it is stable
// across reads and survives a snapshot restore.
func (m *Mock) publicAddress(allocID string) string {
	inUse := make(map[string]bool)
	for _, e := range m.eips.All() {
		inUse[e.PublicIP] = true
	}

	for salt := 0; ; salt++ {
		h := fnv.New32a()
		_, _ = fmt.Fprintf(h, "%s/%d", allocID, salt)
		b := h.Sum(nil)

		// Last octet stays in 1..254 so the address is never a network or
		// broadcast-looking .0/.255.
		addr := fmt.Sprintf("%d.%d.%d.%d", publicIPFirstOctet, b[1], b[2], int(b[3])%publicIPHostOctets+1)
		if !inUse[addr] {
			return addr
		}
	}
}

// AllocateAddress allocates a new public IP address.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the Networking driver interface.
func (m *Mock) AllocateAddress(
	_ context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	allocID := idgen.GenerateID("ipalloc-")

	// Real Azure fills omitted fields with its own defaults (Standard/Regional
	// SKU, Static allocation, IPv4, 4-minute idle timeout) and reports them on GET.
	cfg = applyPublicIPDefaults(cfg)

	eip := &eipData{
		AllocationID:       allocID,
		PublicIP:           m.publicAddress(allocID),
		Tags:               copyTags(cfg.Tags),
		SKU:                cfg.SKU,
		SKUTier:            cfg.SKUTier,
		IPVersion:          cfg.IPVersion,
		AllocationMethod:   cfg.AllocationMethod,
		Zones:              append([]string(nil), cfg.Zones...),
		IdleTimeoutMinutes: cfg.IdleTimeoutMinutes,
		DNSDomainNameLabel: cfg.DNSDomainNameLabel,
		DNSFQDN:            publicIPFQDN(cfg.DNSDomainNameLabel, cfg.Location),
		ResourceGUID:       generateGUID(),
		Location:           cfg.Location,
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
	cfg = applyPublicIPDefaults(cfg)

	found := m.eips.Update(allocationID, func(e *eipData) *eipData {
		cp := *e
		cp.Tags = copyTags(cfg.Tags)
		cp.SKU = cfg.SKU
		cp.SKUTier = cfg.SKUTier
		cp.IPVersion = cfg.IPVersion
		cp.AllocationMethod = cfg.AllocationMethod
		cp.IdleTimeoutMinutes = cfg.IdleTimeoutMinutes
		cp.DNSDomainNameLabel = cfg.DNSDomainNameLabel
		cp.Location = cfg.Location
		cp.DNSFQDN = publicIPFQDN(cfg.DNSDomainNameLabel, cfg.Location)

		cp.Zones = append([]string(nil), cfg.Zones...)

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
		SKUTier:            eip.SKUTier,
		IPVersion:          eip.IPVersion,
		AllocationMethod:   eip.AllocationMethod,
		Zones:              append([]string(nil), eip.Zones...),
		IdleTimeoutMinutes: eip.IdleTimeoutMinutes,
		DNSDomainNameLabel: eip.DNSDomainNameLabel,
		DNSFQDN:            eip.DNSFQDN,
		ResourceGUID:       eip.ResourceGUID,
		Location:           eip.Location,
	}
}
