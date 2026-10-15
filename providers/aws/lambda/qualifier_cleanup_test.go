package lambda

import (
	"context"
	"sync"
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

// TestDeleteAliasNotUndoneByConcurrentUpdate races DeleteAlias against
// UpdateFunction. UpdateFunction copies the function's state, edits it and
// writes it back; without a shared lock it could write back a copy taken
// before DeleteAlias and bring the deleted alias's policy back, so the next
// alias of the same name would inherit the old grant.
func TestDeleteAliasNotUndoneByConcurrentUpdate(t *testing.T) {
	const rounds = 2000

	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PublishVersion(ctx, "my-func", "v1"); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	alias := driver.AliasConfig{FunctionName: "my-func", Name: "live", FunctionVersion: "1"}
	grant := driver.PermissionStatement{StatementID: "old-grant", Action: "lambda:InvokeFunction", Principal: "*"}

	for i := range rounds {
		if _, err := m.CreateAlias(ctx, alias); err != nil {
			t.Fatalf("round %d CreateAlias: %v", i, err)
		}

		if err := m.AddPermission(ctx, "my-func", "live", grant); err != nil {
			t.Fatalf("round %d AddPermission: %v", i, err)
		}

		var wg sync.WaitGroup

		start := make(chan struct{})

		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start

			_ = m.DeleteAlias(ctx, "my-func", "live")
		}()

		go func() {
			defer wg.Done()
			<-start

			_, _ = m.UpdateFunction(ctx, "my-func", driver.FunctionConfig{Timeout: 9})
		}()

		close(start)
		wg.Wait()

		if _, err := m.CreateAlias(ctx, alias); err != nil {
			t.Fatalf("round %d re-CreateAlias: %v", i, err)
		}

		if _, stmts, _ := m.PolicyStatements(ctx, "my-func", "live"); len(stmts) != 0 {
			t.Fatalf("round %d: recreated alias inherited %v", i, stmts)
		}

		if err := m.DeleteAlias(ctx, "my-func", "live"); err != nil {
			t.Fatalf("round %d DeleteAlias: %v", i, err)
		}
	}
}
