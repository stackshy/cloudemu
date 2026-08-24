package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func makeAllowDoc(action, resource string) string {
	return makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": action, "Resource": resource},
	})
}

func TestSimulatePrincipalPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{Name: "r"})
	requireNoError(t, err)

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "p",
		PolicyDocument: makeAllowDoc("s3:ListBucket", "*"),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachRolePolicy(ctx, "r", pol.ARN))

	results, err := m.SimulatePrincipalPolicy(ctx, "arn:aws:iam::123456789012:role/r",
		[]string{"s3:ListBucket", "s3:DeleteObject"}, []string{"arn:aws:s3:::b"}, nil)
	requireNoError(t, err)

	assertEqual(t, 2, len(results))
	assertEqual(t, "allowed", decisionOf(results, "s3:ListBucket"))
	assertEqual(t, "implicitDeny", decisionOf(results, "s3:DeleteObject"))
}

func TestSimulatePrincipalPolicyWithGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "u"})
	requireNoError(t, err)
	_, err = m.CreateGroup(ctx, driver.GroupConfig{Name: "g"})
	requireNoError(t, err)
	requireNoError(t, m.AddUserToGroup(ctx, "u", "g"))

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name:           "gp",
		PolicyDocument: makeAllowDoc("ec2:DescribeInstances", "*"),
	})
	requireNoError(t, err)
	requireNoError(t, m.AttachGroupPolicy(ctx, "g", pol.ARN))

	// The user inherits the group's policy in the simulation.
	results, err := m.SimulatePrincipalPolicy(ctx, "arn:aws:iam::123456789012:user/u",
		[]string{"ec2:DescribeInstances"}, nil, nil)
	requireNoError(t, err)
	assertEqual(t, "allowed", decisionOf(results, "ec2:DescribeInstances"))
}

func TestSimulateCustomPolicyExplicitDeny(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	deny := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"},
	})

	results, err := m.SimulateCustomPolicy(ctx, []string{deny},
		[]string{"s3:GetObject", "s3:DeleteObject"}, nil)
	requireNoError(t, err)

	// No resources supplied -> defaults to "*".
	assertEqual(t, "*", results[0].ResourceName)
	assertEqual(t, "allowed", decisionOf(results, "s3:GetObject"))
	assertEqual(t, "explicitDeny", decisionOf(results, "s3:DeleteObject"))
}

func decisionOf(results []driver.SimulationResult, action string) string {
	for i := range results {
		if results[i].ActionName == action {
			return results[i].Decision
		}
	}

	return ""
}
