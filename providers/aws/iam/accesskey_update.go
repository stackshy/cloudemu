package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// accessKeyStatusActive and accessKeyStatusInactive are the only two states an
// access key can be in (IAM statusType).
const (
	accessKeyStatusActive   = "Active"
	accessKeyStatusInactive = "Inactive"
)

// UpdateAccessKey changes the status of an access key to Active or Inactive
// (IAM UpdateAccessKey). It exists for the wire layer and is not part of the
// portable driver.
func (m *Mock) UpdateAccessKey(_ context.Context, userName, accessKeyID, status string) error {
	if status != accessKeyStatusActive && status != accessKeyStatusInactive {
		return errors.Newf(errors.InvalidArgument,
			"status %q is not one of %q or %q", status, accessKeyStatusActive, accessKeyStatusInactive)
	}

	ak, ok := m.accessKeys.Get(accessKeyID)
	if !ok || ak.UserName != userName {
		return errors.Newf(errors.NotFound, "access key %q not found for user %q", accessKeyID, userName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ak.Status = status
	m.accessKeys.Set(accessKeyID, ak)

	return nil
}
