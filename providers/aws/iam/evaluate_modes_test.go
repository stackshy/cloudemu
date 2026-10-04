package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func polStmt(effect string, fields map[string]any) map[string]any {
	s := map[string]any{"Effect": effect}
	for k, v := range fields {
		s[k] = v
	}

	return s
}

func polDoc(stmts ...map[string]any) string { return makePolicyDoc(stmts) }

func assertDecision(t *testing.T, want, got string) {
	t.Helper()

	if want != got {
		t.Errorf("decision: want %s, got %s", want, got)
	}
}

// userWithDocs creates a user carrying one inline policy per document.
func userWithDocs(t *testing.T, m *Mock, name string, docs ...string) {
	t.Helper()
	requireNoError(t, mustUser(t, m, name))

	for i, d := range docs {
		requireNoError(t, m.PutUserPolicy(context.Background(), name, name+string(rune('a'+i)), d))
	}
}

func unknownRes(action string, cctx ConditionContext) evalRequest {
	return evalRequest{action: action, cctx: cctx}
}

// legacyDecide is the decide loop as it stood before evaluation modes existed,
// kept here as the reference known-resource mode must reproduce.
func legacyDecide(docs []string, action, resource string, cctx ConditionContext) string {
	allow, deny := false, false

	for _, d := range docs {
		a, dn := evaluatePolicy(d, action, resource, cctx)
		deny = deny || dn
		allow = allow || a
	}

	switch {
	case deny:
		return decisionExplicitDeny
	case allow:
		return decisionAllowed
	default:
		return decisionImplicitDeny
	}
}

func TestDecideWithKnownResourceMatchesLegacy(t *testing.T) {
	docs := []string{
		polDoc(polStmt("Allow", map[string]any{"Action": "s3:*", "Resource": "*"})),
		polDoc(polStmt("Allow", map[string]any{"Action": "s3:GetObject", "Resource": "arn:aws:s3:::b/*"})),
		polDoc(polStmt("Allow", map[string]any{"NotAction": "iam:*", "Resource": "*"})),
		polDoc(polStmt("Allow", map[string]any{"Action": "*", "NotResource": "arn:aws:s3:::secret"})),
		polDoc(
			polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"}),
			polStmt("Deny", map[string]any{"Action": "s3:DeleteBucket", "Resource": "arn:aws:s3:::prod"}),
		),
		polDoc(polStmt("Deny", map[string]any{
			"Action": "*", "Resource": "*",
			"Condition": map[string]any{"StringEquals": map[string]any{"s3:prefix": "home/"}},
		})),
		polDoc(polStmt("Allow", map[string]any{
			"Action": "ec2:*", "Resource": "*",
			"Condition": map[string]any{"IpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
		})),
		`not json`,
	}
	actions := []string{"s3:GetObject", "s3:DeleteBucket", "iam:CreateUser", "ec2:RunInstances"}
	resources := []string{"*", "arn:aws:s3:::b/k", "arn:aws:s3:::prod", "arn:aws:s3:::secret"}
	contexts := []ConditionContext{nil, {"aws:SourceIp": "10.1.1.1"}, {"s3:prefix": "home/"}}

	for i := range docs {
		for j := i; j < len(docs); j++ {
			set := []string{docs[i], docs[j]}
			for _, a := range actions {
				for _, r := range resources {
					for _, c := range contexts {
						want := legacyDecide(set, a, r, c)
						got := decideWith(set, evalRequest{action: a, resource: r, cctx: c}, evalKnownResource)
						if want != got {
							t.Fatalf("docs %d,%d %s on %s ctx %v: legacy %s, decideWith %s", i, j, a, r, c, want, got)
						}
					}
				}
			}
		}
	}
}

func TestUnknownResourceAllow(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"star resource counts", polDoc(polStmt("Allow", map[string]any{"Action": "s3:GetObject", "Resource": "*"})), decisionAllowed},
		{"star in list counts", polDoc(polStmt("Allow", map[string]any{
			"Action": "s3:GetObject", "Resource": []any{"arn:aws:s3:::b/*", "*"},
		})), decisionAllowed},
		{"specific resource does not count", polDoc(polStmt("Allow", map[string]any{
			"Action": "s3:GetObject", "Resource": "arn:aws:s3:::b/*",
		})), decisionImplicitDeny},
		{"partial wildcard does not count", polDoc(polStmt("Allow", map[string]any{
			"Action": "s3:GetObject", "Resource": "arn:aws:s3:::*",
		})), decisionImplicitDeny},
		{"NotResource never counts", polDoc(polStmt("Allow", map[string]any{
			"Action": "s3:GetObject", "NotResource": "arn:aws:s3:::secret",
		})), decisionImplicitDeny},
		{"action mismatch", polDoc(polStmt("Allow", map[string]any{"Action": "s3:PutObject", "Resource": "*"})), decisionImplicitDeny},
		{"NotAction allow", polDoc(polStmt("Allow", map[string]any{"NotAction": "iam:*", "Resource": "*"})), decisionAllowed},
		{"NotAction excludes action", polDoc(polStmt("Allow", map[string]any{"NotAction": "s3:*", "Resource": "*"})), decisionImplicitDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertDecision(t, tc.want, decideWith([]string{tc.doc}, unknownRes("s3:GetObject", nil), evalUnknownResource))
		})
	}
}

func TestUnknownResourceDenyIgnoresResource(t *testing.T) {
	allowAll := polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})

	tests := []struct {
		name string
		deny map[string]any
		want string
	}{
		{"specific resource deny counts", polStmt("Deny", map[string]any{
			"Action": "s3:DeleteBucket", "Resource": "arn:aws:s3:::prod",
		}), decisionExplicitDeny},
		{"NotResource deny counts", polStmt("Deny", map[string]any{
			"Action": "s3:DeleteBucket", "NotResource": "arn:aws:s3:::scratch",
		}), decisionExplicitDeny},
		{"NotAction deny counts", polStmt("Deny", map[string]any{
			"NotAction": "s3:Get*", "Resource": "arn:aws:s3:::prod",
		}), decisionExplicitDeny},
		{"other action deny ignored", polStmt("Deny", map[string]any{
			"Action": "s3:PutObject", "Resource": "arn:aws:s3:::prod",
		}), decisionAllowed},
		{"NotAction covering action ignored", polStmt("Deny", map[string]any{
			"NotAction": "s3:*", "Resource": "*",
		}), decisionAllowed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideWith([]string{polDoc(allowAll, tc.deny)}, unknownRes("s3:DeleteBucket", nil), evalUnknownResource)
			assertDecision(t, tc.want, got)
		})
	}
}

// TestUnknownResourceMissingConditionKey covers the fail-closed deviation: with
// an unknown resource, a Deny whose condition key is absent still applies, and
// an Allow whose key is absent does not.
func TestUnknownResourceMissingConditionKey(t *testing.T) {
	allowAll := polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})
	prefixCond := map[string]any{"StringEquals": map[string]any{"s3:prefix": "home/"}}
	denyPrefix := polStmt("Deny", map[string]any{"Action": "s3:ListBucket", "Resource": "*", "Condition": prefixCond})
	allowPrefix := polStmt("Allow", map[string]any{"Action": "s3:ListBucket", "Resource": "*", "Condition": prefixCond})
	allowIfExists := polStmt("Allow", map[string]any{
		"Action": "s3:ListBucket", "Resource": "*",
		"Condition": map[string]any{"StringEqualsIfExists": map[string]any{"s3:prefix": "home/"}},
	})
	denyIPAndPrefix := polStmt("Deny", map[string]any{
		"Action": "s3:ListBucket", "Resource": "*",
		"Condition": map[string]any{
			"IpAddress":    map[string]any{"aws:SourceIp": "10.0.0.0/8"},
			"StringEquals": map[string]any{"s3:prefix": "home/"},
		},
	})

	tests := []struct {
		name string
		docs []string
		cctx ConditionContext
		mode evalMode
		want string
	}{
		{"deny with missing key counts", []string{polDoc(allowAll, denyPrefix)}, nil, evalUnknownResource, decisionExplicitDeny},
		{"deny with present non-matching key ignored", []string{polDoc(allowAll, denyPrefix)},
			ConditionContext{"s3:prefix": "other/"}, evalUnknownResource, decisionAllowed},
		{"deny present key still ANDs with missing key", []string{polDoc(allowAll, denyIPAndPrefix)},
			ConditionContext{"aws:SourceIp": "10.1.2.3"}, evalUnknownResource, decisionExplicitDeny},
		{"deny present key failing wins over missing key", []string{polDoc(allowAll, denyIPAndPrefix)},
			ConditionContext{"aws:SourceIp": "8.8.8.8"}, evalUnknownResource, decisionAllowed},
		{"allow with missing key ignored", []string{polDoc(allowPrefix)}, nil, evalUnknownResource, decisionImplicitDeny},
		{"allow IfExists with missing key ignored", []string{polDoc(allowIfExists)}, nil, evalUnknownResource, decisionImplicitDeny},
		{"allow with present matching key counts", []string{polDoc(allowPrefix)},
			ConditionContext{"s3:prefix": "home/"}, evalUnknownResource, decisionAllowed},
		{"known mode keeps real IAM semantics for deny", []string{polDoc(allowAll, denyPrefix)}, nil, evalKnownResource, decisionAllowed},
		{"known mode keeps IfExists allow", []string{polDoc(allowIfExists)}, nil, evalKnownResource, decisionAllowed},
		{"service-wide deny with missing key counts", []string{polDoc(allowAll, denyPrefix)}, nil, evalServiceWide, decisionExplicitDeny},
		{"service-wide allow with missing key ignored", []string{polDoc(allowPrefix)}, nil, evalServiceWide, decisionImplicitDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := evalRequest{action: "s3:ListBucket", resource: "*", service: "s3", cctx: tc.cctx}
			assertDecision(t, tc.want, decideWith(tc.docs, req, tc.mode))
		})
	}
}

func TestServiceWideDecisions(t *testing.T) {
	allowAll := polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})
	allow := func(field string, v any) map[string]any {
		return polStmt("Allow", map[string]any{field: v, "Resource": "*"})
	}
	deny := func(field string, v any) map[string]any {
		return polStmt("Deny", map[string]any{field: v, "Resource": "arn:aws:s3:::prod"})
	}

	tests := []struct {
		name  string
		stmts []map[string]any
		svc   string
		want  string
	}{
		{"allow star", []map[string]any{allowAll}, "s3", decisionAllowed},
		{"allow svc star", []map[string]any{allow("Action", "s3:*")}, "s3", decisionAllowed},
		{"allow prefix wildcard", []map[string]any{allow("Action", "s*")}, "s3", decisionAllowed},
		{"allow svc star in list", []map[string]any{allow("Action", []any{"s3:GetObject", "s3:*"})}, "s3", decisionAllowed},
		{"allow partial action", []map[string]any{allow("Action", "s3:Get*")}, "s3", decisionImplicitDeny},
		{"allow other service", []map[string]any{allow("Action", "ec2:*")}, "s3", decisionImplicitDeny},
		{"allow svc star on specific resource", []map[string]any{
			polStmt("Allow", map[string]any{"Action": "s3:*", "Resource": "arn:aws:s3:::b"}),
		}, "s3", decisionImplicitDeny},
		{"allow NotResource", []map[string]any{
			polStmt("Allow", map[string]any{"Action": "s3:*", "NotResource": "arn:aws:s3:::b"}),
		}, "s3", decisionImplicitDeny},
		{"allow NotAction other service", []map[string]any{allow("NotAction", []any{"iam:*", "organizations:*"})}, "s3", decisionAllowed},
		{"allow NotAction same service", []map[string]any{allow("NotAction", []any{"iam:*", "organizations:*"})}, "iam", decisionImplicitDeny},
		{"allow NotAction partial same service", []map[string]any{allow("NotAction", "s3:DeleteBucket")}, "s3", decisionImplicitDeny},
		{"allow NotAction star", []map[string]any{allow("NotAction", "*")}, "s3", decisionImplicitDeny},
		{"allow NotAction service wildcard", []map[string]any{allow("NotAction", "s*:*")}, "s3", decisionImplicitDeny},
		{"deny one action", []map[string]any{allowAll, deny("Action", "s3:DeleteBucket")}, "s3", decisionExplicitDeny},
		{"deny wildcard service", []map[string]any{allowAll, deny("Action", "*:Delete*")}, "s3", decisionExplicitDeny},
		{"deny pattern without colon", []map[string]any{allowAll, deny("Action", "s3*")}, "s3", decisionExplicitDeny},
		{"deny other service", []map[string]any{allowAll, deny("Action", "ec2:*")}, "s3", decisionAllowed},
		{"deny NotResource", []map[string]any{allowAll, polStmt("Deny", map[string]any{
			"Action": "s3:PutObject", "NotResource": "arn:aws:s3:::b/*",
		})}, "s3", decisionExplicitDeny},
		{"deny NotAction covers svc", []map[string]any{allowAll, deny("NotAction", "s3:*")}, "s3", decisionAllowed},
		{"deny NotAction star covers svc", []map[string]any{allowAll, deny("NotAction", "*")}, "s3", decisionAllowed},
		{"deny NotAction partial", []map[string]any{allowAll, deny("NotAction", "s3:Get*")}, "s3", decisionExplicitDeny},
		{"deny NotAction other service", []map[string]any{allowAll, deny("NotAction", "iam:*")}, "s3", decisionExplicitDeny},
		{"no statements", nil, "s3", decisionImplicitDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			docs := []string{}
			if tc.stmts != nil {
				docs = append(docs, polDoc(tc.stmts...))
			}

			assertDecision(t, tc.want, decideWith(docs, evalRequest{service: tc.svc}, evalServiceWide))
		})
	}
}

func TestEvaluatePermissionTriState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	userWithDocs(t, m, "ann", polDoc(
		polStmt("Allow", map[string]any{"Action": "s3:*", "Resource": "*"}),
		polStmt("Deny", map[string]any{"Action": "s3:DeleteBucket", "Resource": "arn:aws:s3:::prod"}),
	))

	tests := []struct {
		name string
		req  driver.EvalRequest
		want driver.Decision
	}{
		{"known allowed", driver.EvalRequest{
			Principal: "ann", Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k", ResourceKnown: true,
		}, driver.DecisionAllowed},
		{"known implicit deny", driver.EvalRequest{
			Principal: "ann", Action: "ec2:RunInstances", Resource: "*", ResourceKnown: true,
		}, driver.DecisionImplicitDeny},
		{"known explicit deny", driver.EvalRequest{
			Principal: "ann", Action: "s3:DeleteBucket", Resource: "arn:aws:s3:::prod", ResourceKnown: true,
		}, driver.DecisionExplicitDeny},
		{"known other bucket allowed", driver.EvalRequest{
			Principal: "ann", Action: "s3:DeleteBucket", Resource: "arn:aws:s3:::dev", ResourceKnown: true,
		}, driver.DecisionAllowed},
		{"unknown resource deny applies", driver.EvalRequest{
			Principal: "ann", Action: "s3:DeleteBucket",
		}, driver.DecisionExplicitDeny},
		{"unknown resource allowed", driver.EvalRequest{Principal: "ann", Action: "s3:GetObject"}, driver.DecisionAllowed},
		{"unknown principal", driver.EvalRequest{
			Principal: "ghost", Action: "s3:GetObject", Resource: "*", ResourceKnown: true,
		}, driver.DecisionImplicitDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertEqual(t, tc.want, m.EvaluatePermission(ctx, tc.req))
		})
	}

	assertEqual(t, driver.DecisionExplicitDeny, m.EvaluateServiceWide(ctx, "ann", "s3", nil))
	assertEqual(t, driver.DecisionImplicitDeny, m.EvaluateServiceWide(ctx, "ann", "ec2", nil))
	assertEqual(t, driver.DecisionImplicitDeny, m.EvaluateServiceWide(ctx, "ghost", "s3", nil))
}

// TestEvaluateExplicitDenyBeatsAllow proves a Deny in one policy source (a
// group) overrides an Allow in another (an inline user policy) in every mode.
func TestEvaluateExplicitDenyBeatsAllow(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	userWithDocs(t, m, "bea", polDoc(polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})))
	requireNoError(t, mustGroup(t, m, "locked"))
	requireNoError(t, m.PutGroupPolicy(ctx, "locked", "deny-iam", polDoc(
		polStmt("Deny", map[string]any{"Action": "iam:*", "Resource": "*"}),
	)))
	requireNoError(t, m.AddUserToGroup(ctx, "bea", "locked"))

	assertEqual(t, driver.DecisionExplicitDeny, m.EvaluatePermission(ctx, driver.EvalRequest{
		Principal: "bea", Action: "iam:CreateUser", Resource: "*", ResourceKnown: true,
	}))
	assertEqual(t, driver.DecisionExplicitDeny, m.EvaluatePermission(ctx, driver.EvalRequest{
		Principal: "bea", Action: "iam:CreateUser",
	}))
	assertEqual(t, driver.DecisionExplicitDeny, m.EvaluateServiceWide(ctx, "bea", "iam", nil))
	assertEqual(t, driver.DecisionAllowed, m.EvaluateServiceWide(ctx, "bea", "s3", nil))
}

func TestEvaluatePermissionsBoundaryModes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	userWithDocs(t, m, "cal", polDoc(polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})))

	boundary, err := m.CreatePolicy(ctx, driver.PolicyConfig{
		Name: "boundary",
		PolicyDocument: polDoc(
			polStmt("Allow", map[string]any{"Action": "s3:*", "Resource": "arn:aws:s3:::data/*"}),
			polStmt("Allow", map[string]any{"Action": "sqs:*", "Resource": "*"}),
			polStmt("Deny", map[string]any{"Action": "sqs:DeleteQueue", "Resource": "arn:aws:sqs:us-east-1:123456789012:prod"}),
		),
	})
	requireNoError(t, err)
	requireNoError(t, m.PutUserPermissionsBoundary(ctx, "cal", boundary.ARN))

	tests := []struct {
		name string
		req  driver.EvalRequest
		want driver.Decision
	}{
		{"known inside boundary", driver.EvalRequest{
			Principal: "cal", Action: "s3:GetObject", Resource: "arn:aws:s3:::data/x", ResourceKnown: true,
		}, driver.DecisionAllowed},
		{"known outside boundary resource", driver.EvalRequest{
			Principal: "cal", Action: "s3:GetObject", Resource: "arn:aws:s3:::other/x", ResourceKnown: true,
		}, driver.DecisionImplicitDeny},
		{"unknown resource boundary needs star", driver.EvalRequest{Principal: "cal", Action: "s3:GetObject"}, driver.DecisionImplicitDeny},
		{"unknown resource boundary star", driver.EvalRequest{Principal: "cal", Action: "sqs:SendMessage"}, driver.DecisionAllowed},
		{"unknown resource boundary deny", driver.EvalRequest{Principal: "cal", Action: "sqs:DeleteQueue"}, driver.DecisionExplicitDeny},
		{"outside boundary action", driver.EvalRequest{Principal: "cal", Action: "ec2:RunInstances"}, driver.DecisionImplicitDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertEqual(t, tc.want, m.EvaluatePermission(ctx, tc.req))
		})
	}

	assertEqual(t, driver.DecisionImplicitDeny, m.EvaluateServiceWide(ctx, "cal", "s3", nil))
	assertEqual(t, driver.DecisionExplicitDeny, m.EvaluateServiceWide(ctx, "cal", "sqs", nil))
	assertEqual(t, driver.DecisionImplicitDeny, m.EvaluateServiceWide(ctx, "cal", "ec2", nil))

	// CheckPermission keeps treating the boundary as it did before.
	ok, err := m.CheckPermission(ctx, "cal", "s3:GetObject", "arn:aws:s3:::data/x")
	requireNoError(t, err)
	assertEqual(t, true, ok)

	ok, err = m.CheckPermission(ctx, "cal", "s3:GetObject", "arn:aws:s3:::other/x")
	requireNoError(t, err)
	assertEqual(t, false, ok)
}

// TestCheckPermissionAgreesWithKnownEvaluation checks that CheckPermission and
// CheckPermissionWithContext return exactly what they did before, and that the
// known-resource EvaluatePermission agrees with them on the same corpus.
func TestCheckPermissionAgreesWithKnownEvaluation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	userWithDocs(t, m, "dee",
		polDoc(polStmt("Allow", map[string]any{"Action": "s3:GetObject", "Resource": "arn:aws:s3:::allowed/*"})),
		polDoc(polStmt("Allow", map[string]any{
			"Action": "ec2:RunInstances", "Resource": "*",
			"Condition": map[string]any{"IpAddress": map[string]any{"aws:SourceIp": "10.0.0.0/8"}},
		})),
		polDoc(
			polStmt("Allow", map[string]any{"NotAction": "iam:*", "Resource": "*"}),
			polStmt("Deny", map[string]any{"Action": "dynamodb:DeleteTable", "Resource": "*"}),
		),
	)

	tests := []struct {
		action, resource string
		cctx             map[string]string
		want             bool
	}{
		{"s3:GetObject", "arn:aws:s3:::allowed/r.txt", nil, true},
		{"s3:GetObject", "arn:aws:s3:::other/r.txt", nil, true}, // NotAction iam:* allows it
		{"ec2:RunInstances", "*", map[string]string{"aws:SourceIp": "10.9.9.9"}, true},
		{"ec2:RunInstances", "*", nil, true},
		{"iam:CreateUser", "*", nil, false},
		{"iam:CreateUser", "*", map[string]string{"aws:SourceIp": "10.9.9.9"}, false},
		{"dynamodb:DeleteTable", "arn:aws:dynamodb:us-east-1:123456789012:table/t", nil, false},
		{"dynamodb:PutItem", "arn:aws:dynamodb:us-east-1:123456789012:table/t", nil, true},
	}

	for _, tc := range tests {
		got, err := m.CheckPermissionWithContext(ctx, "dee", tc.action, tc.resource, tc.cctx)
		requireNoError(t, err)
		assertEqual(t, tc.want, got)

		if tc.cctx == nil {
			plain, perr := m.CheckPermission(ctx, "dee", tc.action, tc.resource)
			requireNoError(t, perr)
			assertEqual(t, tc.want, plain)
		}

		dec := m.EvaluatePermission(ctx, driver.EvalRequest{
			Principal: "dee", Action: tc.action, Resource: tc.resource, ResourceKnown: true, Context: tc.cctx,
		})
		assertEqual(t, tc.want, dec == driver.DecisionAllowed)
	}
}

func TestCouldMatchService(t *testing.T) {
	tests := []struct {
		pattern, svc string
		want         bool
	}{
		{"*", "s3", true},
		{"s3:*", "s3", true},
		{"s3:GetObject", "s3", true},
		{"*:Get*", "s3", true},
		{"s*:*", "s3", true},
		{"s3*", "s3", true},
		{"s*", "sqs", true},
		{"ec2:*", "s3", false},
		{"s3", "s3", false},
		{"sqs*", "s3", false},
		{"", "s3", false},
	}

	for _, tc := range tests {
		if got := couldMatchService(tc.pattern, tc.svc); got != tc.want {
			t.Errorf("couldMatchService(%q, %q) = %v, want %v", tc.pattern, tc.svc, got, tc.want)
		}
	}
}

func TestServiceWideRejectsMalformedService(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	userWithDocs(t, m, "root-like", polDoc(polStmt("Allow", map[string]any{"Action": "*", "Resource": "*"})))

	for _, svc := range []string{"", "a:b", "s3:*", "*", "s*", "S3", "-s3", "s3 "} {
		assertEqual(t, driver.DecisionImplicitDeny, m.EvaluateServiceWide(ctx, "root-like", svc, nil))
	}

	for _, svc := range []string{"s3", "resource-explorer-2", "ec2"} {
		assertEqual(t, driver.DecisionAllowed, m.EvaluateServiceWide(ctx, "root-like", svc, nil))
	}
}
