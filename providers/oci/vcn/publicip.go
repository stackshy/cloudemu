package vcn

import (
	"context"
	"crypto/sha256"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Public IP lifetimes. OCI's reserved IPs outlive the resource they are
// assigned to; ephemeral ones do not.
const (
	LifetimeReserved  = "RESERVED"
	LifetimeEphemeral = "EPHEMERAL"
)

type publicIPData struct {
	AllocationID string
	Address      string
	// AssignedTo is the private IP OCID the address is assigned to. OCI
	// assigns a public IP to a private IP, not to an instance.
	AssignedTo string
	Lifetime   string
	Tags       map[string]string
}

// AllocateAddress reserves a public IP. cfg.AllocationMethod carries OCI's
// lifetime, defaulting to RESERVED.
func (m *Mock) AllocateAddress(_ context.Context, cfg driver.ElasticIPConfig) (*driver.ElasticIP, error) {
	lifetime := cfg.AllocationMethod
	if lifetime == "" {
		lifetime = LifetimeReserved
	}

	id := m.newOCID(typePublicIP)
	ip := &publicIPData{
		AllocationID: id,
		Address:      mockPublicIP(id),
		Lifetime:     lifetime,
		Tags:         copyTags(cfg.Tags),
	}

	m.publicIPs.Set(id, ip)
	m.record(id)

	info := toPublicIPInfo(ip)

	return &info, nil
}

// ReleaseAddress releases a public IP that is not assigned to anything.
func (m *Mock) ReleaseAddress(_ context.Context, allocationID string) error {
	ip, ok := m.publicIPs.Get(allocationID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "public IP %q not found", allocationID)
	}

	if ip.AssignedTo != "" {
		return cerrors.Newf(cerrors.FailedPrecondition, "public IP %q is still assigned", allocationID)
	}

	m.publicIPs.Delete(allocationID)
	m.forget(allocationID)

	return nil
}

// DescribeAddresses returns public IPs matching the given OCIDs, or all if empty.
func (m *Mock) DescribeAddresses(_ context.Context, ids []string) ([]driver.ElasticIP, error) {
	return describeResources(m.publicIPs, ids, toPublicIPInfo), nil
}

// AssociateAddress assigns a public IP to a private IP. The returned handle is
// the private IP's OCID, which is what OCI clears the assignment by.
func (m *Mock) AssociateAddress(_ context.Context, allocationID, instanceID string) (string, error) {
	ip, ok := m.publicIPs.Get(allocationID)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "public IP %q not found", allocationID)
	}

	if ip.AssignedTo != "" {
		return "", cerrors.Newf(cerrors.FailedPrecondition, "public IP %q is already assigned", allocationID)
	}

	if instanceID == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "private IP OCID is required")
	}

	ip.AssignedTo = instanceID

	return instanceID, nil
}

// DisassociateAddress clears the assignment of the public IP pointing at the
// given private IP.
func (m *Mock) DisassociateAddress(_ context.Context, associationID string) error {
	for _, ip := range m.publicIPs.All() {
		if ip.AssignedTo == associationID {
			ip.AssignedTo = ""

			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "no public IP assigned to %q", associationID)
}

func toPublicIPInfo(ip *publicIPData) driver.ElasticIP {
	return driver.ElasticIP{
		AllocationID:     ip.AllocationID,
		PublicIP:         ip.Address,
		AssociationID:    ip.AssignedTo,
		InstanceID:       ip.AssignedTo,
		Tags:             copyTags(ip.Tags),
		AllocationMethod: ip.Lifetime,
	}
}

// mockPublicIP derives a stable address in OCI's public range from an OCID.
func mockPublicIP(id string) string {
	sum := sha256.Sum256([]byte(id))

	return fmt.Sprintf("129.146.%d.%d", sum[0], sum[1])
}
