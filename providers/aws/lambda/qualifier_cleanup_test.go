package lambda

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// attachQualifierState gives qualifier a resource-policy statement, an event
// invoke config and a provisioned concurrency config, plus a function URL when
// withURL is set (real Lambda only allows a URL on an alias or $LATEST).
func attachQualifierState(t *testing.T, m *Mock, qualifier string, withURL bool) {
	t.Helper()

	ctx := context.Background()

	if err := m.AddPermission(ctx, "my-func", qualifier, driver.PermissionStatement{
		StatementID: "old-grant", Action: "lambda:InvokeFunction", Principal: "111111111111",
	}); err != nil {
		t.Fatalf("AddPermission(%s): %v", qualifier, err)
	}

	if withURL {
		if _, err := m.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{
			FunctionName: "my-func", Qualifier: qualifier, AuthType: "NONE",
		}); err != nil {
			t.Fatalf("CreateFunctionURLConfig(%s): %v", qualifier, err)
		}
	}

	retries := 1
	if _, err := m.PutFunctionEventInvokeConfig(ctx, driver.EventInvokeConfig{
		FunctionName: "my-func", Qualifier: qualifier, MaximumRetryAttempts: &retries,
	}); err != nil {
		t.Fatalf("PutFunctionEventInvokeConfig(%s): %v", qualifier, err)
	}

	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName: "my-func", Qualifier: qualifier, RequestedProvisionedConcurrentExecutions: 1,
	}); err != nil {
		t.Fatalf("PutFunctionProvisionedConcurrencyConfig(%s): %v", qualifier, err)
	}
}

func assertQualifierStateGone(t *testing.T, m *Mock, qualifier string) {
	t.Helper()

	ctx := context.Background()

	if _, err := m.GetPolicy(ctx, "my-func", qualifier); !errors.IsNotFound(err) {
		t.Fatalf("GetPolicy(%s) err = %v, want NotFound", qualifier, err)
	}

	if _, err := m.GetFunctionURLConfig(ctx, "my-func", qualifier); !errors.IsNotFound(err) {
		t.Fatalf("GetFunctionURLConfig(%s) err = %v, want NotFound", qualifier, err)
	}

	if _, err := m.GetFunctionEventInvokeConfig(ctx, "my-func", qualifier); !errors.IsNotFound(err) {
		t.Fatalf("GetFunctionEventInvokeConfig(%s) err = %v, want NotFound", qualifier, err)
	}

	if _, err := m.GetFunctionProvisionedConcurrencyConfig(ctx, "my-func", qualifier); !errors.IsNotFound(err) {
		t.Fatalf("GetFunctionProvisionedConcurrencyConfig(%s) err = %v, want NotFound", qualifier, err)
	}
}

func TestDeleteAliasDropsQualifierState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	alias := driver.AliasConfig{FunctionName: "my-func", Name: "live", FunctionVersion: "1"}
	if _, err := m.CreateAlias(ctx, alias); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	attachQualifierState(t, m, "live", true)

	// Unqualified state must survive the alias delete.
	if err := m.AddPermission(ctx, "my-func", "", driver.PermissionStatement{
		StatementID: "fn-grant", Action: "lambda:InvokeFunction", Principal: "222222222222",
	}); err != nil {
		t.Fatalf("AddPermission(unqualified): %v", err)
	}

	if err := m.DeleteAlias(ctx, "my-func", "live"); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}

	if _, err := m.CreateAlias(ctx, alias); err != nil {
		t.Fatalf("re-CreateAlias: %v", err)
	}

	assertQualifierStateGone(t, m, "live")

	if _, stmts, _ := m.PolicyStatements(ctx, "my-func", "live"); len(stmts) != 0 {
		t.Fatalf("PolicyStatements(live) after recreate = %v, want none", stmts)
	}

	if _, err := m.GetPolicy(ctx, "my-func", ""); err != nil {
		t.Fatalf("GetPolicy(unqualified) after alias delete: %v", err)
	}
}

func TestDeleteVersionDropsQualifierState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	attachQualifierState(t, m, "1", false)

	if err := m.DeleteVersion(ctx, "my-func", "1"); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	assertQualifierStateGone(t, m, "1")
}
