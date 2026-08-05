package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Network interface status values.
const (
	ENIStatusAvailable = "available"
	ENIStatusInUse     = "in-use"
)

type eniData struct {
	ID           string
	VPCID        string
	SubnetID     string
	Status       string
	AttachmentID string
	Description  string
	Tags         map[string]string
}

// CreateNetworkInterface creates a standalone, unattached ENI in the given
// subnet (ec2:CreateNetworkInterface). The VPC is resolved from the subnet, so
// an unknown subnet is NotFound.
func (m *Mock) CreateNetworkInterface(
	_ context.Context, subnetID, description string, tags map[string]string,
) (*driver.NetworkInterface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subnets.Get(subnetID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "InvalidSubnetID.NotFound: subnet %q not found", subnetID)
	}

	id := idgen.GenerateID("eni-")
	eni := &eniData{
		ID:          id,
		VPCID:       sub.VPCID,
		SubnetID:    subnetID,
		Status:      ENIStatusAvailable,
		Description: description,
		Tags:        copyTags(tags),
	}
	m.enis.Set(id, eni)

	info := toENIInfo(eni)

	return &info, nil
}

// DescribeNetworkInterfaces returns ENIs matching the given IDs, or all if empty.
//
// An explicitly named ID that does not exist is NotFound rather than an empty
// list: a caller draining a VPC reads an empty answer as "already gone" and
// moves on, which is the wrong conclusion when it asked about one specific
// interface.
func (m *Mock) DescribeNetworkInterfaces(_ context.Context, ids []string) ([]driver.NetworkInterface, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range ids {
		if !m.enis.Has(id) {
			return nil, errors.Newf(errors.NotFound,
				"InvalidNetworkInterfaceID.NotFound: network interface %q not found", id)
		}
	}

	return describeResources(m.enis, ids, toENIInfo), nil
}

// DetachNetworkInterface detaches the ENI carrying the given attachment ID.
//
// force is accepted and ignored: the emulator has no in-flight traffic for a
// forced detach to interrupt, so the distinction has no observable effect.
func (m *Mock) DetachNetworkInterface(_ context.Context, attachmentID string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, eni := range m.enis.All() {
		if eni.AttachmentID != attachmentID {
			continue
		}

		eni.AttachmentID = ""
		eni.Status = ENIStatusAvailable

		return nil
	}

	return errors.Newf(errors.NotFound, "network interface attachment %q not found", attachmentID)
}

// DeleteNetworkInterface deletes the ENI with the given ID.
//
// An attached ENI cannot be deleted — real EC2 answers DependencyViolation,
// and callers rely on that to know a drain is still in progress rather than
// complete.
func (m *Mock) DeleteNetworkInterface(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eni, ok := m.enis.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "network interface %q not found", id)
	}

	if eni.AttachmentID != "" {
		return errors.Newf(errors.FailedPrecondition,
			"network interface %q is still attached", id)
	}

	m.enis.Delete(id)

	return nil
}

// attachManagedENI records the interface a managed resource (NAT gateway,
// load balancer, managed database) occupies while it exists. Callers deleting
// a VPC must drain these before the VPC delete is accepted, so the emulator
// has to actually hold one rather than report an empty set.
func (m *Mock) attachManagedENI(vpcID, subnetID, description string) *eniData {
	id := idgen.GenerateID("eni-")

	eni := &eniData{
		ID:           id,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		Status:       ENIStatusInUse,
		AttachmentID: idgen.GenerateID("eni-attach-"),
		Description:  description,
	}
	m.enis.Set(id, eni)

	return eni
}

// releaseManagedENIs drops the interfaces held by a managed resource. Real AWS
// releases them asynchronously, which is the race the caller's drain loop
// exists to absorb; the emulator releases synchronously so teardown is
// deterministic.
func (m *Mock) releaseManagedENIs(description string) {
	for id, eni := range m.enis.All() {
		if eni.Description == description {
			m.enis.Delete(id)
		}
	}
}

func toENIInfo(e *eniData) driver.NetworkInterface {
	return driver.NetworkInterface{
		ID:           e.ID,
		VPCID:        e.VPCID,
		SubnetID:     e.SubnetID,
		Status:       e.Status,
		AttachmentID: e.AttachmentID,
		Description:  e.Description,
		Tags:         copyTags(e.Tags),
	}
}
