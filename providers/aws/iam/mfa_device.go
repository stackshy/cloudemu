package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// mfaDeviceData is a stored MFA device. UserName is empty until the virtual
// device is assigned to a user via EnableMFADevice (not yet emulated), so a
// freshly created virtual device is unassigned and absent from ListMFADevices.
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
