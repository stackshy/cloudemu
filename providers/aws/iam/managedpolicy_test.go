package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func newIAM(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions())
}

func mkRole(t *testing.T, m *Mock, name string) {
	t.Helper()

	if _, err := m.CreateRole(context.Background(), driver.RoleConfig{
		Name:                name,
		AssumeRolePolicyDoc: `{"Version":"2012-10-17","Statement":[]}`,
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
}

// Every real AWS account already has the AWS-managed policies, so callers
// attach them without a preceding CreatePolicy. Requiring one turns an
// ordinary AttachRolePolicy into NoSuchEntity and stops any flow that gives an
// instance profile SSM access.
func TestAttachAWSManagedPolicyWithoutCreate(t *testing.T) {
	ctx := context.Background()
	m := newIAM(t)
	mkRole(t, m, "vm-role")

	const arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"

	if err := m.AttachRolePolicy(ctx, "vm-role", arn); err != nil {
		t.Fatalf("AttachRolePolicy: %v", err)
	}

	attached, err := m.ListAttachedRolePolicies(ctx, "vm-role")
	if err != nil {
		t.Fatalf("ListAttachedRolePolicies: %v", err)
	}

	if len(attached) != 1 || attached[0] != arn {
		t.Errorf("attached = %v, want [%s]", attached, arn)
	}
}

// The catalog covers the policies real callers attach, pathed ones included.
func TestAttachCataloguedAWSManagedPolicies(t *testing.T) {
	ctx := context.Background()
	m := newIAM(t)
	mkRole(t, m, "r")

	for _, arn := range []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
		"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
		"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
		"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
		"arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy",
		"arn:aws:iam::aws:policy/service-role/AmazonEC2RoleforSSM",
	} {
		if err := m.AttachRolePolicy(ctx, "r", arn); err != nil {
			t.Errorf("AttachRolePolicy(%s): %v", arn, err)
		}
	}
}

// AWS publishes a fixed set, so a name outside it is NoSuchEntity in a real
// account. Accepting anything well-formed would let a typo through — the
// emulator would attach AmazonEKSClusterPolicyy and the caller would only find
// out in production.
func TestAttachUnknownAWSManagedPolicyFails(t *testing.T) {
	ctx := context.Background()
	m := newIAM(t)
	mkRole(t, m, "r")

	for _, arn := range []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicyy",
		"arn:aws:iam::aws:policy/TotallyInvented",
	} {
		if err := m.AttachRolePolicy(ctx, "r", arn); err == nil {
			t.Errorf("attaching %s should fail — no such AWS managed policy", arn)
		}
	}
}

// A pathed managed ARN keeps its path and takes the last segment as its name.
func TestManagedPolicyPathIsPreserved(t *testing.T) {
	ctx := context.Background()
	m := newIAM(t)

	const arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"

	got, err := m.GetPolicy(ctx, arn)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if got.Name != "AmazonEBSCSIDriverPolicy" {
		t.Errorf("name = %q, want AmazonEBSCSIDriverPolicy", got.Name)
	}

	if got.Path != "/service-role/" {
		t.Errorf("path = %q, want /service-role/", got.Path)
	}
}

// A customer-managed ARN that was never created must still be NotFound —
// materializing those on demand would hide a genuine caller bug.
func TestNonManagedPolicyStillNotFound(t *testing.T) {
	ctx := context.Background()
	m := newIAM(t)
	mkRole(t, m, "r")

	err := m.AttachRolePolicy(ctx, "r", "arn:aws:iam::123456789012:policy/NeverCreated")
	if err == nil {
		t.Error("attaching an uncreated customer-managed policy should fail")
	}
}
