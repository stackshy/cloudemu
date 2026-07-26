package gcpvpc

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

// DescribeNetworkInterfaces returns interfaces matching the given IDs, or all if empty.
func (m *Mock) DescribeNetworkInterfaces(_ context.Context, ids []string) ([]driver.NetworkInterface, error) {
	return describeResources(m.enis, ids, toENIInfo), nil
}

// DetachNetworkInterface detaches the interface carrying the given attachment ID.
//
// force is accepted and ignored: the emulator has no in-flight traffic for a
// forced detach to interrupt, so the distinction has no observable effect.
func (m *Mock) DetachNetworkInterface(_ context.Context, attachmentID string, _ bool) error {
	for _, eni := range m.enis.All() {
		if eni.AttachmentID != attachmentID {
			continue
		}

		eni.AttachmentID = ""
		eni.Status = ENIStatusAvailable

		return nil
	}

	return cerrors.Newf(cerrors.NotFound, "network interface attachment %q not found", attachmentID)
}

// DeleteNetworkInterface deletes the interface with the given ID. An attached
// interface cannot be deleted, so a caller draining a network can tell the
// difference between "gone" and "still in use".
func (m *Mock) DeleteNetworkInterface(_ context.Context, id string) error {
	eni, ok := m.enis.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "network interface %q not found", id)
	}

	if eni.AttachmentID != "" {
		return cerrors.Newf(cerrors.FailedPrecondition, "network interface %q is still attached", id)
	}

	m.enis.Delete(id)

	return nil
}

// attachManagedENI records the interface a managed resource occupies while it
// exists; releaseManagedENIs drops it again when that resource goes away.
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

func (m *Mock) releaseManagedENIs(description string) {
	for id, eni := range m.enis.All() {
		if eni.Description == description {
			m.enis.Delete(id)
		}
	}
}

func natENIDescription(natID string) string {
	return "Interface for NAT Gateway " + natID
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
