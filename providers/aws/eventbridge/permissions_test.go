package eventbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

func requireNoErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// busPolicy reads the default bus policy through the public GetEventBus path.
func busPolicy(t *testing.T, m *Mock) string {
	t.Helper()

	info, err := m.GetEventBus(context.Background(), defaultBusName)
	requireNoErr(t, err)

	return info.Policy
}

// policyStatements decodes the rendered policy document into its statement list.
func policyStatements(t *testing.T, policy string) []map[string]any {
	t.Helper()

	if policy == "" {
		return nil
	}

	var doc struct {
		Version   string           `json:"Version"`
		Statement []map[string]any `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatalf("policy is not valid JSON: %v (%s)", err, policy)
	}

	return doc.Statement
}

func TestPutPermissionLegacy(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	err := m.PutPermission(ctx, "", driver.PermissionInput{
		StatementID: "acct-123",
		Action:      "events:PutEvents",
		Principal:   "123456789012",
	})
	requireNoErr(t, err)

	stmts := policyStatements(t, busPolicy(t, m))
	if len(stmts) != 1 {
		t.Fatalf("want 1 statement, got %d", len(stmts))
	}

	if stmts[0]["Sid"] != "acct-123" || stmts[0]["Action"] != "events:PutEvents" {
		t.Fatalf("unexpected statement: %#v", stmts[0])
	}

	principal, ok := stmts[0]["Principal"].(map[string]any)
	if !ok || principal["AWS"] != "arn:aws:iam::123456789012:root" {
		t.Fatalf("unexpected principal: %#v", stmts[0]["Principal"])
	}
}

func TestPutPermissionWildcardPrincipal(t *testing.T) {
	m, _ := newTestMock()

	err := m.PutPermission(context.Background(), "", driver.PermissionInput{
		StatementID: "everyone",
		Action:      "events:PutEvents",
		Principal:   "*",
	})
	requireNoErr(t, err)

	stmts := policyStatements(t, busPolicy(t, m))
	if stmts[0]["Principal"] != "*" {
		t.Fatalf("want wildcard principal, got %#v", stmts[0]["Principal"])
	}
}

func TestPutPermissionCondition(t *testing.T) {
	m, _ := newTestMock()

	err := m.PutPermission(context.Background(), "", driver.PermissionInput{
		StatementID: "org-only",
		Action:      "events:PutEvents",
		Principal:   "*",
		Condition:   &driver.PermissionCondition{Type: "StringEquals", Key: "aws:PrincipalOrgID", Value: "o-abc123"},
	})
	requireNoErr(t, err)

	policy := busPolicy(t, m)
	if !strings.Contains(policy, "aws:PrincipalOrgID") || !strings.Contains(policy, "o-abc123") {
		t.Fatalf("condition not reflected in policy: %s", policy)
	}

	cond, ok := policyStatements(t, policy)[0]["Condition"].(map[string]any)
	if !ok {
		t.Fatalf("Condition missing: %#v", policyStatements(t, policy)[0])
	}

	se, ok := cond["StringEquals"].(map[string]any)
	if !ok || se["aws:PrincipalOrgID"] != "o-abc123" {
		t.Fatalf("unexpected condition shape: %#v", cond)
	}
}

func TestPutPermissionPolicyDocument(t *testing.T) {
	m, _ := newTestMock()

	doc := `{"Version":"2012-10-17","Statement":[` +
		`{"Sid":"a","Effect":"Allow","Principal":"*","Action":"events:PutEvents","Resource":"arn"},` +
		`{"Sid":"b","Effect":"Allow","Principal":"*","Action":"events:PutEvents","Resource":"arn"}]}`

	requireNoErr(t, m.PutPermission(context.Background(), "", driver.PermissionInput{Policy: doc}))

	stmts := policyStatements(t, busPolicy(t, m))
	if len(stmts) != 2 {
		t.Fatalf("want 2 statements from policy doc, got %d", len(stmts))
	}

	// A subsequent full-policy PutPermission replaces the whole policy.
	single := `{"Statement":{"Sid":"c","Effect":"Allow","Principal":"*","Action":"events:PutEvents","Resource":"arn"}}`
	requireNoErr(t, m.PutPermission(context.Background(), "", driver.PermissionInput{Policy: single}))

	stmts = policyStatements(t, busPolicy(t, m))
	if len(stmts) != 1 || stmts[0]["Sid"] != "c" {
		t.Fatalf("policy doc did not replace prior policy: %#v", stmts)
	}
}

func TestPutPermissionUpsertBySid(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	in := driver.PermissionInput{StatementID: "s1", Action: "events:PutEvents", Principal: "111111111111"}
	requireNoErr(t, m.PutPermission(ctx, "", in))

	in.Principal = "222222222222"
	requireNoErr(t, m.PutPermission(ctx, "", in))

	stmts := policyStatements(t, busPolicy(t, m))
	if len(stmts) != 1 {
		t.Fatalf("want statement replaced in place, got %d statements", len(stmts))
	}

	principal, _ := stmts[0]["Principal"].(map[string]any)
	if principal["AWS"] != "arn:aws:iam::222222222222:root" {
		t.Fatalf("statement not updated: %#v", stmts[0])
	}
}

func TestPutPermissionValidation(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	// neither legacy trio nor Policy
	requireErr(t, m.PutPermission(ctx, "", driver.PermissionInput{}))

	// both legacy and Policy
	requireErr(t, m.PutPermission(ctx, "", driver.PermissionInput{
		StatementID: "s", Action: "events:PutEvents", Principal: "*", Policy: `{"Statement":[]}`,
	}))

	// wrong action
	requireErr(t, m.PutPermission(ctx, "", driver.PermissionInput{
		StatementID: "s", Action: "events:PutRule", Principal: "*",
	}))

	// unknown bus
	requireErr(t, m.PutPermission(ctx, "missing", driver.PermissionInput{
		StatementID: "s", Action: "events:PutEvents", Principal: "*",
	}))
}

func TestRemovePermission(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	requireNoErr(t, m.PutPermission(ctx, "", driver.PermissionInput{StatementID: "s1", Action: "events:PutEvents", Principal: "*"}))
	requireNoErr(t, m.PutPermission(ctx, "", driver.PermissionInput{StatementID: "s2", Action: "events:PutEvents", Principal: "*"}))

	// remove by id
	requireNoErr(t, m.RemovePermission(ctx, "", "s1", false))

	stmts := policyStatements(t, busPolicy(t, m))
	if len(stmts) != 1 || stmts[0]["Sid"] != "s2" {
		t.Fatalf("remove by id failed: %#v", stmts)
	}

	// unknown id -> NotFound
	requireErr(t, m.RemovePermission(ctx, "", "nope", false))

	// remove all clears the policy
	requireNoErr(t, m.RemovePermission(ctx, "", "", true))
	if pol := busPolicy(t, m); pol != "" {
		t.Fatalf("want empty policy after RemoveAll, got %q", pol)
	}
}

func TestPutRuleRejectsEmptyPattern(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	if _, err := m.PutRule(ctx, &driver.RuleConfig{Name: "r", EventPattern: "{}"}); err == nil {
		t.Fatal("PutRule with empty {} pattern should fail")
	}

	if _, err := m.PutRule(ctx, &driver.RuleConfig{Name: "ok", EventPattern: `{"source":["aws.ec2"]}`}); err != nil {
		t.Fatalf("PutRule with valid pattern should succeed: %v", err)
	}
}
