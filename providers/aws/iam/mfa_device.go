package iam

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// mfaDeviceData is a stored MFA device. UserName is empty until the virtual
// device is assigned to a user via EnableMFADevice, so a freshly created
// virtual device is unassigned and absent from ListMFADevices.
type mfaDeviceData struct {
	SerialNumber string
	UserName     string
	EnableDate   string
	Base32Seed   []byte
	QRCodePNG    []byte
}

// CreateVirtualMFADevice creates an unassigned virtual MFA device (IAM
// CreateVirtualMFADevice).
func (m *Mock) CreateVirtualMFADevice(_ context.Context, name, _ string) (*driver.VirtualMFADeviceInfo, error) {
	if name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "VirtualMFADeviceName is required")
	}

	serial := idgen.AWSARN("iam", "", m.opts.AccountID, "mfa/"+name)

	if m.mfaDevices.Has(serial) {
		return nil, errors.Newf(errors.AlreadyExists, "MFA device %q already exists", serial)
	}

	seed := []byte("CLOUDEMU" + idgen.GenerateID(""))
	qr := []byte("cloudemu-qr-" + name)

	d := &mfaDeviceData{
		SerialNumber: serial,
		Base32Seed:   seed,
		QRCodePNG:    qr,
	}
	m.mfaDevices.Set(serial, d)

	return &driver.VirtualMFADeviceInfo{
		SerialNumber:     serial,
		Base32StringSeed: seed,
		QRCodePNG:        qr,
	}, nil
}

// ListMFADevices returns the MFA devices assigned to a user (IAM
// ListMFADevices). A user with no assigned device gets an empty list.
func (m *Mock) ListMFADevices(_ context.Context, userName string) ([]driver.MFADeviceInfo, error) {
	if !m.users.Has(userName) {
		return nil, errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	var result []driver.MFADeviceInfo

	for _, d := range m.mfaDevices.All() {
		if d.UserName != userName {
			continue
		}

		result = append(result, driver.MFADeviceInfo{
			UserName:     d.UserName,
			SerialNumber: d.SerialNumber,
			EnableDate:   d.EnableDate,
		})
	}

	return result, nil
}

// EnableMFADevice associates a virtual MFA device with a user (IAM
// EnableMFADevice). Real IAM requires two consecutive, distinct codes from the
// device; this emulator does not verify the codes against the seed but still
// rejects an empty or identical pair, matching AWS's InvalidInput shape for
// obviously malformed input. A device already enabled for a different user is
// rejected as EntityAlreadyExists; re-enabling for the same user just refreshes
// EnableDate.
func (m *Mock) EnableMFADevice(_ context.Context, userName, serialNumber, authCode1, authCode2 string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	if authCode1 == "" || authCode2 == "" {
		return errors.Newf(errors.InvalidArgument, "AuthenticationCode1 and AuthenticationCode2 are required")
	}

	if authCode1 == authCode2 {
		return errors.Newf(errors.InvalidArgument,
			"AuthenticationCode1 and AuthenticationCode2 must be two consecutive, different codes from the device")
	}

	now := m.opts.Clock.Now().UTC().Format(timeFormat)

	var conflict error

	ok := m.mfaDevices.Update(serialNumber, func(d *mfaDeviceData) *mfaDeviceData {
		if d.UserName != "" && d.UserName != userName {
			conflict = errors.Newf(errors.AlreadyExists,
				"MFA device %q is already assigned to user %q", serialNumber, d.UserName)

			return d
		}

		d.UserName = userName
		d.EnableDate = now

		return d
	})
	if !ok {
		return errors.Newf(errors.NotFound, "MFA device %q not found", serialNumber)
	}

	return conflict
}

// DeactivateMFADevice removes a virtual MFA device's association with a user
// (IAM DeactivateMFADevice). The device record itself survives, unassigned,
// so it can be re-enabled or deleted afterward.
func (m *Mock) DeactivateMFADevice(_ context.Context, userName, serialNumber string) error {
	if !m.users.Has(userName) {
		return errors.Newf(errors.NotFound, "user %q not found", userName)
	}

	var mismatch bool

	ok := m.mfaDevices.Update(serialNumber, func(d *mfaDeviceData) *mfaDeviceData {
		if d.UserName != userName {
			mismatch = true

			return d
		}

		d.UserName = ""
		d.EnableDate = ""

		return d
	})
	if !ok || mismatch {
		return errors.Newf(errors.NotFound, "MFA device %q not found for user %q", serialNumber, userName)
	}

	return nil
}

// DeleteVirtualMFADevice removes a virtual MFA device (IAM
// DeleteVirtualMFADevice). Deleting a device still enabled for a user is
// rejected as DeleteConflict; DeactivateMFADevice must run first. The check
// and the delete happen under a single store lock via UpdateOrDelete so a
// concurrent EnableMFADevice cannot race a delete of the same device.
func (m *Mock) DeleteVirtualMFADevice(_ context.Context, serialNumber string) error {
	var conflict error

	ok := m.mfaDevices.UpdateOrDelete(serialNumber, func(d *mfaDeviceData) (*mfaDeviceData, bool) {
		if d.UserName != "" {
			conflict = errors.Newf(errors.FailedPrecondition,
				"cannot delete MFA device %q: it is still enabled for user %q (deactivate it first)",
				serialNumber, d.UserName)

			return d, true
		}

		return d, false
	})
	if !ok {
		return errors.Newf(errors.NotFound, "MFA device %q not found", serialNumber)
	}

	return conflict
}

// mfaAssignmentStatusAssigned, mfaAssignmentStatusUnassigned, and
// mfaAssignmentStatusAny are the three values IAM's ListVirtualMFADevices
// AssignmentStatus filter accepts; an empty filter behaves as Any.
const (
	mfaAssignmentStatusAssigned   = "Assigned"
	mfaAssignmentStatusUnassigned = "Unassigned"
	mfaAssignmentStatusAny        = "Any"
)

// ListVirtualMFADevices returns every virtual MFA device in the account,
// optionally filtered by assignment status (IAM ListVirtualMFADevices).
func (m *Mock) ListVirtualMFADevices(_ context.Context, assignmentStatus string) ([]driver.VirtualMFADeviceMetadata, error) {
	switch assignmentStatus {
	case "", mfaAssignmentStatusAny, mfaAssignmentStatusAssigned, mfaAssignmentStatusUnassigned:
	default:
		return nil, errors.Newf(errors.InvalidArgument, "invalid AssignmentStatus %q", assignmentStatus)
	}

	var result []driver.VirtualMFADeviceMetadata

	for _, d := range m.mfaDevices.All() {
		assigned := d.UserName != ""

		switch assignmentStatus {
		case mfaAssignmentStatusAssigned:
			if !assigned {
				continue
			}
		case mfaAssignmentStatusUnassigned:
			if assigned {
				continue
			}
		}

		meta := driver.VirtualMFADeviceMetadata{SerialNumber: d.SerialNumber, EnableDate: d.EnableDate}

		if assigned {
			if u, ok := m.users.Get(d.UserName); ok {
				info := toUserInfo(u)
				meta.AssignedUser = &info
			}
		}

		result = append(result, meta)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].SerialNumber < result[j].SerialNumber })

	return result, nil
}

// mfaCountsLocked returns the total number of virtual MFA devices and how many
// are assigned to a user. The caller must hold m.mu.
func (m *Mock) mfaCountsLocked() (total, inUse int) {
	for _, d := range m.mfaDevices.All() {
		total++

		if d.UserName != "" {
			inUse++
		}
	}

	return total, inUse
}
