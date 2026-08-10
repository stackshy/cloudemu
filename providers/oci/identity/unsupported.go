package identity

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// What OCI does instead of the operation being asked for.
const (
	viaStatements        = "an OCI policy grants access through its statements, not by attachment"
	viaInstancePrincipal = "an OCI instance takes its identity from a dynamic group matching rule"
)

// unsupported reports an operation with no OCI equivalent.
func unsupported(operation, instead string) error {
	return cerrors.Newf(cerrors.Unimplemented, "%s is not an OCI operation: %s", operation, instead)
}

// AttachUserPolicy is not an OCI operation.
func (*Mock) AttachUserPolicy(_ context.Context, _, _ string) error {
	return unsupported("AttachUserPolicy", viaStatements)
}

// DetachUserPolicy is not an OCI operation.
func (*Mock) DetachUserPolicy(_ context.Context, _, _ string) error {
	return unsupported("DetachUserPolicy", viaStatements)
}

// AttachRolePolicy is not an OCI operation.
func (*Mock) AttachRolePolicy(_ context.Context, _, _ string) error {
	return unsupported("AttachRolePolicy", viaStatements)
}

// DetachRolePolicy is not an OCI operation.
func (*Mock) DetachRolePolicy(_ context.Context, _, _ string) error {
	return unsupported("DetachRolePolicy", viaStatements)
}

// ListAttachedUserPolicies is not an OCI operation.
func (*Mock) ListAttachedUserPolicies(_ context.Context, _ string) ([]string, error) {
	return nil, unsupported("ListAttachedUserPolicies", viaStatements)
}

// ListAttachedRolePolicies is not an OCI operation.
func (*Mock) ListAttachedRolePolicies(_ context.Context, _ string) ([]string, error) {
	return nil, unsupported("ListAttachedRolePolicies", viaStatements)
}

// CreateInstanceProfile is not an OCI operation.
func (*Mock) CreateInstanceProfile(
	_ context.Context, _ driver.InstanceProfileConfig,
) (*driver.InstanceProfileInfo, error) {
	return nil, unsupported("CreateInstanceProfile", viaInstancePrincipal)
}

// DeleteInstanceProfile is not an OCI operation.
func (*Mock) DeleteInstanceProfile(_ context.Context, _ string) error {
	return unsupported("DeleteInstanceProfile", viaInstancePrincipal)
}

// GetInstanceProfile is not an OCI operation.
func (*Mock) GetInstanceProfile(_ context.Context, _ string) (*driver.InstanceProfileInfo, error) {
	return nil, unsupported("GetInstanceProfile", viaInstancePrincipal)
}

// ListInstanceProfiles is not an OCI operation.
func (*Mock) ListInstanceProfiles(_ context.Context) ([]driver.InstanceProfileInfo, error) {
	return nil, unsupported("ListInstanceProfiles", viaInstancePrincipal)
}

// AddRoleToInstanceProfile is not an OCI operation.
func (*Mock) AddRoleToInstanceProfile(_ context.Context, _, _ string) error {
	return unsupported("AddRoleToInstanceProfile", viaInstancePrincipal)
}

// RemoveRoleFromInstanceProfile is not an OCI operation.
func (*Mock) RemoveRoleFromInstanceProfile(_ context.Context, _, _ string) error {
	return unsupported("RemoveRoleFromInstanceProfile", viaInstancePrincipal)
}
