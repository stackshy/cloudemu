package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// decideDoc evaluates a single policy document for one action/resource with the
// given request context, returning the simulation decision.
func decideDoc(doc, action, resource string, cctx ConditionContext) string {
	return decide([]string{doc}, action, resource, cctx)
}

// TestNotActionDeny proves a Deny with NotAction blocks every action EXCEPT the
// listed one (fail-closed), while the listed action is still allowed.
func TestNotActionDeny(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "NotAction": "s3:GetObject", "Resource": "*"},
	})

	// The listed action is exempt from the Deny, so it stays allowed.
	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:GetObject", "*", nil))

	// Every other action falls into the NotAction complement and is denied.
	assertEqual(t, decisionExplicitDeny, decideDoc(doc, "s3:PutObject", "*", nil))
	assertEqual(t, decisionExplicitDeny, decideDoc(doc, "s3:DeleteObject", "*", nil))
}

// TestNotActionAllow proves an Allow with NotAction grants every action except
// the listed ones.
func TestNotActionAllow(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "NotAction": []any{"iam:*", "sts:*"}, "Resource": "*"},
	})

	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:GetObject", "*", nil))
	assertEqual(t, decisionImplicitDeny, decideDoc(doc, "iam:CreateUser", "*", nil))
}

// TestNotResource proves NotResource matches every resource except the listed
// ones, for both Deny and Allow.
func TestNotResource(t *testing.T) {
	deny := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "Action": "s3:*", "NotResource": "arn:aws:s3:::safe/*"},
	})

	// A resource inside the exempted set is not denied.
	assertEqual(t, decisionAllowed, decideDoc(deny, "s3:GetObject", "arn:aws:s3:::safe/report.txt", nil))
	// A resource outside it falls into the complement and is denied.
	assertEqual(t, decisionExplicitDeny, decideDoc(deny, "s3:GetObject", "arn:aws:s3:::other/x", nil))
}

// TestConditionOperators exercises each supported condition operator against a
// supplied request context.
func TestConditionOperators(t *testing.T) {
	tests := []struct {
		name    string
		cond    map[string]any
		cctx    ConditionContext
		allowed bool
	}{
		{
			name:    "StringEquals match",
			cond:    map[string]any{"StringEquals": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{"aws:username": "bob"},
			allowed: true,
		},
		{
			name:    "StringEquals mismatch",
			cond:    map[string]any{"StringEquals": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{"aws:username": "alice"},
			allowed: false,
		},
		{
			name:    "StringNotEquals blocks listed",
			cond:    map[string]any{"StringNotEquals": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{"aws:username": "bob"},
			allowed: false,
		},
		{
			name:    "StringLike wildcard",
			cond:    map[string]any{"StringLike": map[string]any{"aws:PrincipalArn": "arn:aws:iam::*:user/dev-*"}},
			cctx:    ConditionContext{"aws:PrincipalArn": "arn:aws:iam::123:user/dev-bob"},
			allowed: true,
		},
		{
			name:    "StringEqualsIgnoreCase",
			cond:    map[string]any{"StringEqualsIgnoreCase": map[string]any{"aws:username": "BOB"}},
			cctx:    ConditionContext{"aws:username": "bob"},
			allowed: true,
		},
		{
			name:    "Bool secure transport true",
			cond:    map[string]any{"Bool": map[string]any{"aws:SecureTransport": "true"}},
			cctx:    ConditionContext{"aws:SecureTransport": "true"},
			allowed: true,
		},
		{
			name:    "Bool secure transport denied",
			cond:    map[string]any{"Bool": map[string]any{"aws:SecureTransport": "true"}},
			cctx:    ConditionContext{"aws:SecureTransport": "false"},
			allowed: false,
		},
		{
			name:    "NumericLessThan allows below",
			cond:    map[string]any{"NumericLessThan": map[string]any{"s3:max-keys": "100"}},
			cctx:    ConditionContext{"s3:max-keys": "50"},
			allowed: true,
		},
		{
			name:    "NumericLessThan denies at/above",
			cond:    map[string]any{"NumericLessThan": map[string]any{"s3:max-keys": "100"}},
			cctx:    ConditionContext{"s3:max-keys": "150"},
			allowed: false,
		},
		{
			name:    "IpAddress inside CIDR",
			cond:    map[string]any{"IpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
			cctx:    ConditionContext{"aws:SourceIp": "10.1.2.3"},
			allowed: true,
		},
		{
			name:    "IpAddress outside CIDR",
			cond:    map[string]any{"IpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
			cctx:    ConditionContext{"aws:SourceIp": "192.168.1.1"},
			allowed: false,
		},
		{
			name:    "NotIpAddress blocks inside CIDR",
			cond:    map[string]any{"NotIpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
			cctx:    ConditionContext{"aws:SourceIp": "10.1.2.3"},
			allowed: false,
		},
		{
			name:    "ArnLike match",
			cond:    map[string]any{"ArnLike": map[string]any{"aws:PrincipalArn": "arn:aws:iam::*:role/app-*"}},
			cctx:    ConditionContext{"aws:PrincipalArn": "arn:aws:iam::123:role/app-web"},
			allowed: true,
		},
		{
			name:    "DateGreaterThan after",
			cond:    map[string]any{"DateGreaterThan": map[string]any{"aws:CurrentTime": "2020-01-01T00:00:00Z"}},
			cctx:    ConditionContext{"aws:CurrentTime": "2025-06-01T00:00:00Z"},
			allowed: true,
		},
		{
			name:    "DateGreaterThan before denies",
			cond:    map[string]any{"DateGreaterThan": map[string]any{"aws:CurrentTime": "2020-01-01T00:00:00Z"}},
			cctx:    ConditionContext{"aws:CurrentTime": "2019-01-01T00:00:00Z"},
			allowed: false,
		},
		{
			name:    "IfExists passes when key absent",
			cond:    map[string]any{"StringEqualsIfExists": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{},
			allowed: true,
		},
		{
			name:    "IfExists still enforces when key present",
			cond:    map[string]any{"StringEqualsIfExists": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{"aws:username": "alice"},
			allowed: false,
		},
		{
			name:    "plain condition fails when key absent",
			cond:    map[string]any{"StringEquals": map[string]any{"aws:username": "bob"}},
			cctx:    ConditionContext{},
			allowed: false,
		},
		{
			name:    "Null false requires key present",
			cond:    map[string]any{"Null": map[string]any{"aws:username": "false"}},
			cctx:    ConditionContext{"aws:username": "bob"},
			allowed: true,
		},
		{
			name:    "Null false denies when key absent",
			cond:    map[string]any{"Null": map[string]any{"aws:username": "false"}},
			cctx:    ConditionContext{},
			allowed: false,
		},
		{
			name:    "Null true requires key absent",
			cond:    map[string]any{"Null": map[string]any{"aws:MultiFactorAuthPresent": "true"}},
			cctx:    ConditionContext{},
			allowed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := makePolicyDoc([]map[string]any{
				{"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": "*", "Condition": tc.cond},
			})

			got := decideDoc(doc, "ec2:RunInstances", "*", tc.cctx)

			want := decisionImplicitDeny
			if tc.allowed {
				want = decisionAllowed
			}

			assertEqual(t, want, got)
		})
	}
}

// TestConditionMultipleKeysAnd proves that multiple keys under one operator must
// all be satisfied (AWS AND semantics).
func TestConditionMultipleKeysAnd(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{
			"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": "*",
			"Condition": map[string]any{"StringEquals": map[string]any{
				"aws:username":        "bob",
				"aws:RequestedRegion": "us-east-1",
			}},
		},
	})

	both := ConditionContext{"aws:username": "bob", "aws:RequestedRegion": "us-east-1"}
	assertEqual(t, decisionAllowed, decideDoc(doc, "ec2:RunInstances", "*", both))

	wrongRegion := ConditionContext{"aws:username": "bob", "aws:RequestedRegion": "eu-west-1"}
	assertEqual(t, decisionImplicitDeny, decideDoc(doc, "ec2:RunInstances", "*", wrongRegion))
}

// TestConditionValueListOr proves multiple values for one key combine with OR.
func TestConditionValueListOr(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{
			"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": "*",
			"Condition": map[string]any{"StringEquals": map[string]any{
				"aws:RequestedRegion": []any{"us-east-1", "us-west-2"},
			}},
		},
	})

	assertEqual(t, decisionAllowed, decideDoc(doc, "ec2:RunInstances", "*",
		ConditionContext{"aws:RequestedRegion": "us-west-2"}))
	assertEqual(t, decisionImplicitDeny, decideDoc(doc, "ec2:RunInstances", "*",
		ConditionContext{"aws:RequestedRegion": "eu-west-1"}))
}

// TestCheckPermissionWithContext proves the integration path evaluates a
// Condition-guarded policy against the supplied request context.
func TestCheckPermissionWithContext(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "bob"})
	requireNoError(t, err)

	doc := makePolicyDoc([]map[string]any{
		{
			"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": "*",
			"Condition": map[string]any{"IpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
		},
	})

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "p", PolicyDocument: doc})
	requireNoError(t, err)
	requireNoError(t, m.AttachUserPolicy(ctx, "bob", pol.ARN))

	allowed, err := m.CheckPermissionWithContext(ctx, "bob", "ec2:RunInstances", "*",
		map[string]string{"aws:SourceIp": "10.9.9.9"})
	requireNoError(t, err)
	assertEqual(t, true, allowed)

	denied, err := m.CheckPermissionWithContext(ctx, "bob", "ec2:RunInstances", "*",
		map[string]string{"aws:SourceIp": "8.8.8.8"})
	requireNoError(t, err)
	assertEqual(t, false, denied)

	// CheckPermission (no context) sees the guarded statement as unmatched.
	plain, err := m.CheckPermission(ctx, "bob", "ec2:RunInstances", "*")
	requireNoError(t, err)
	assertEqual(t, false, plain)
}

// TestCheckPermissionResourceScoped proves a resource-scoped policy allows the
// matching resource and denies a non-matching one.
func TestCheckPermissionResourceScoped(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "carol"})
	requireNoError(t, err)

	doc := makeAllowDoc("s3:GetObject", "arn:aws:s3:::allowed/*")
	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "rs", PolicyDocument: doc})
	requireNoError(t, err)
	requireNoError(t, m.AttachUserPolicy(ctx, "carol", pol.ARN))

	allowed, err := m.CheckPermission(ctx, "carol", "s3:GetObject", "arn:aws:s3:::allowed/report.txt")
	requireNoError(t, err)
	assertEqual(t, true, allowed)

	denied, err := m.CheckPermission(ctx, "carol", "s3:GetObject", "arn:aws:s3:::other/report.txt")
	requireNoError(t, err)
	assertEqual(t, false, denied)
}

// TestSimulateReflectsCondition proves SimulatePrincipalPolicy honors the
// request context passed through ContextEntries, including NotAction Deny.
func TestSimulateReflectsCondition(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateUser(ctx, driver.UserConfig{Name: "dave"})
	requireNoError(t, err)

	doc := makePolicyDoc([]map[string]any{
		{
			"Effect": "Allow", "Action": "ec2:RunInstances", "Resource": "*",
			"Condition": map[string]any{"StringEquals": map[string]any{"aws:username": "dave"}},
		},
		{"Effect": "Deny", "NotAction": "ec2:RunInstances", "Resource": "*"},
	})

	pol, err := m.CreatePolicy(ctx, driver.PolicyConfig{Name: "sp", PolicyDocument: doc})
	requireNoError(t, err)
	requireNoError(t, m.AttachUserPolicy(ctx, "dave", pol.ARN))

	src := "arn:aws:iam::123456789012:user/dave"

	// With the matching context, the conditioned Allow applies.
	res, err := m.SimulatePrincipalPolicy(ctx, src, []string{"ec2:RunInstances"}, nil, nil,
		map[string]string{"aws:username": "dave"})
	requireNoError(t, err)
	assertEqual(t, decisionAllowed, decisionOf(res, "ec2:RunInstances"))

	// A different context fails the condition, so the Allow no longer applies.
	res, err = m.SimulatePrincipalPolicy(ctx, src, []string{"ec2:RunInstances"}, nil, nil,
		map[string]string{"aws:username": "eve"})
	requireNoError(t, err)
	assertEqual(t, decisionImplicitDeny, decisionOf(res, "ec2:RunInstances"))

	// The NotAction Deny blocks any other action (fail-closed).
	res, err = m.SimulatePrincipalPolicy(ctx, src, []string{"ec2:TerminateInstances"}, nil, nil,
		map[string]string{"aws:username": "dave"})
	requireNoError(t, err)
	assertEqual(t, decisionExplicitDeny, decisionOf(res, "ec2:TerminateInstances"))
}
