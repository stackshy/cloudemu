package iam

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// UpdateRole updates a role's Description and/or MaxSessionDuration in place
// (IAM UpdateRole). Nil pointers leave the corresponding field unchanged, which
// matches how the SDK omits unset optional parameters. It exists for the wire
// layer's UpdateRole action and is not part of the portable driver.
func (m *Mock) UpdateRole(_ context.Context, roleName string, description *string, maxSessionDuration *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	if description != nil {
		rd.Description = *description
	}

	if maxSessionDuration != nil {
		rd.MaxSessionDuration = *maxSessionDuration
	}

	m.roles.Set(roleName, rd)

	return nil
}

// UpdateAssumeRolePolicy replaces a role's trust policy document (IAM
// UpdateAssumeRolePolicy). It exists for the wire layer and is not part of the
// portable driver.
func (m *Mock) UpdateAssumeRolePolicy(_ context.Context, roleName, policyDocument string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.roles.Get(roleName)
	if !ok {
		return errors.Newf(errors.NotFound, "role %q not found", roleName)
	}

	rd.AssumeRolePolicyDoc = policyDocument
	m.roles.Set(roleName, rd)

	return nil
}
