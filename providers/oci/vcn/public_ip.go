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
		return publicIPNotFound(allocationID)
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
// the private IP's OCID, which is what OCI clears the assignment by. A private
// IP holds at most one public IP, and DisassociateAddress takes that handle,
// so a second address on the same private IP would be unaddressable.
func (m *Mock) AssociateAddress(_ context.Context, allocationID, instanceID string) (string, error) {
	if instanceID == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "private IP OCID is required")
	}

	if !m.privateIPs.Has(instanceID) {
		return "", privateIPNotFound(instanceID)
	}

	// Read before the write: publicIPFor reads the same store, which cannot be
	// done from inside its own Update.
	if held := m.publicIPFor(instanceID); held != "" {
		return "", cerrors.Newf(cerrors.FailedPrecondition,
			"private IP %q already has public IP %s", instanceID, held)
	}

	err := mutate(m.publicIPs, allocationID, publicIPNotFound(allocationID), func(ip *publicIPData) error {
		if ip.AssignedTo != "" {
			return cerrors.Newf(cerrors.FailedPrecondition, "public IP %q is already assigned", allocationID)
		}

		ip.AssignedTo = instanceID

		return nil
	})
	if err != nil {
		return "", err
	}

	return instanceID, nil
}

// DisassociateAddress clears the assignment of the public IP pointing at the
// given private IP.
func (m *Mock) DisassociateAddress(_ context.Context, associationID string) error {
	for _, held := range m.publicIPs.All() {
		if held.AssignedTo != associationID {
			continue
		}

		return mutate(m.publicIPs, held.AllocationID, publicIPNotFound(held.AllocationID),
			func(ip *publicIPData) error {
				ip.AssignedTo = ""

				return nil
			})
	}

	return cerrors.Newf(cerrors.NotFound, "no public IP assigned to %q", associationID)
}

func publicIPNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "public IP %q not found", id)
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
