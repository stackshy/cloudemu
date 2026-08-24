package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

type eipData struct {
	AllocationID       string
	PublicIP           string
	AssociationID      string
	InstanceID         string
	NetworkInterfaceID string
	PrivateIP          string
	Tags               map[string]string
}

// AllocateAddress allocates a new elastic IP address.
func (m *Mock) AllocateAddress(
	_ context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	allocID := idgen.GenerateID("eipalloc-")

	eip := &eipData{
		AllocationID: allocID,
		PublicIP:     mockPublicIP(allocID),
		Tags:         copyTags(cfg.Tags),
	}
	m.eips.Set(allocID, eip)

	info := toEIPInfo(eip)

	return &info, nil
}

// ReleaseAddress releases an elastic IP address.
func (m *Mock) ReleaseAddress(
	_ context.Context, allocationID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eip, ok := m.eips.Get(allocationID)
	if !ok {
		return errors.Newf(
			errors.NotFound,
			"elastic IP %q not found", allocationID,
		)
	}

	if eip.AssociationID != "" {
		return errors.Newf(
			errors.FailedPrecondition,
			"elastic IP %q is still associated", allocationID,
		)
	}

	m.eips.Delete(allocationID)

	return nil
}

// DescribeAddresses returns elastic IPs matching the given
// allocation IDs, or all if ids is empty.
func (m *Mock) DescribeAddresses(
	_ context.Context, ids []string,
) ([]driver.ElasticIP, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.eips, ids, toEIPInfo), nil
}

// AssociateAddress binds an elastic IP to an instance or, when
// in.NetworkInterfaceID is set, to a specific network interface (optionally at
// a secondary private address). Real EC2 rejects an unknown interface with
// InvalidNetworkInterfaceID.NotFound; the networking mock does not model
// instances, so an unknown instance is validated by the caller (the wire
// layer), not here.
func (m *Mock) AssociateAddress(
	_ context.Context, allocationID string, in driver.AssociateAddressInput,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	eip, ok := m.eips.Get(allocationID)
	if !ok {
		return "", errors.Newf(
			errors.NotFound,
			"elastic IP %q not found", allocationID,
		)
	}

	if in.NetworkInterfaceID != "" {
		if !m.enis.Has(in.NetworkInterfaceID) {
			return "", errors.Newf(errors.NotFound,
				"InvalidNetworkInterfaceID.NotFound: network interface %q not found", in.NetworkInterfaceID)
		}
	}

	// Real EC2 remaps automatically (reassociation is the default), so re-associating
	// an already-bound EIP replaces the target. An explicit AllowReassociation=false
	// makes the call fail when the EIP is already bound to a different target.
	if eip.AssociationID != "" && in.AllowReassociation != nil && !*in.AllowReassociation &&
		!sameEIPTarget(eip, in) {
		return "", errors.Newf(
			errors.AlreadyExists,
			"Resource.AlreadyAssociated: elastic IP %q is already associated", allocationID,
		)
	}

	// A fresh association id is returned each time.
	assocID := idgen.GenerateID("eipassoc-")
	eip.AssociationID = assocID
	eip.InstanceID = in.InstanceID
	eip.NetworkInterfaceID = in.NetworkInterfaceID
	eip.PrivateIP = in.PrivateIP

	return assocID, nil
}

// DisassociateAddress removes an elastic IP association.
func (m *Mock) DisassociateAddress(
	_ context.Context, associationID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, eip := range m.eips.All() {
		if eip.AssociationID != associationID {
			continue
		}

		eip.AssociationID = ""
		eip.InstanceID = ""
		eip.NetworkInterfaceID = ""
		eip.PrivateIP = ""

		return nil
	}

	return errors.Newf(
		errors.NotFound,
		"association %q not found", associationID,
	)
}

// sameEIPTarget reports whether the requested association points at the target
// the EIP already holds, in which case a strict re-associate is idempotent.
func sameEIPTarget(eip *eipData, in driver.AssociateAddressInput) bool {
	return eip.InstanceID == in.InstanceID && eip.NetworkInterfaceID == in.NetworkInterfaceID
}

func toEIPInfo(eip *eipData) driver.ElasticIP {
	return driver.ElasticIP{
		AllocationID:       eip.AllocationID,
		PublicIP:           eip.PublicIP,
		AssociationID:      eip.AssociationID,
		InstanceID:         eip.InstanceID,
		NetworkInterfaceID: eip.NetworkInterfaceID,
		PrivateIP:          eip.PrivateIP,
		Tags:               copyTags(eip.Tags),
	}
}
