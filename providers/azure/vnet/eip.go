package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

type eipData struct {
	AllocationID  string
	PublicIP      string
	AssociationID string
	InstanceID    string
	Tags          map[string]string
}

// AllocateAddress allocates a new public IP address.
func (m *Mock) AllocateAddress(
	_ context.Context, cfg driver.ElasticIPConfig,
) (*driver.ElasticIP, error) {
	allocID := idgen.GenerateID("ipalloc-")

	eip := &eipData{
		AllocationID: allocID,
		PublicIP:     mockPublicIP(allocID),
		Tags:         copyTags(cfg.Tags),
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
	_ context.Context, allocationID, instanceID string,
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
	eip.InstanceID = instanceID

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
		AllocationID:  eip.AllocationID,
		PublicIP:      eip.PublicIP,
		AssociationID: eip.AssociationID,
		InstanceID:    eip.InstanceID,
		Tags:          copyTags(eip.Tags),
	}
}
