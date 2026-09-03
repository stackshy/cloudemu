package lambda

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// createPublishedFunc creates my-func and publishes version "1", returning that
// version.
func createPublishedFunc(t *testing.T, m *Mock) *driver.FunctionVersion {
	t.Helper()

	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	ver, err := m.PublishVersion(ctx, "my-func", "v1")
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	return ver
}

func TestProvisionedConcurrencyRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	ver := createPublishedFunc(t, m)

	put, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                ver.Version,
		RequestedProvisionedConcurrentExecutions: 5,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if put.Status != statusReady {
		t.Fatalf("Status = %q, want READY", put.Status)
	}

	if put.RequestedProvisionedConcurrentExecutions != 5 ||
		put.AvailableProvisionedConcurrentExecutions != 5 ||
		put.AllocatedProvisionedConcurrentExecutions != 5 {
		t.Fatalf("Put = %+v, want requested=available=allocated=5", put)
	}

	got, err := m.GetFunctionProvisionedConcurrencyConfig(ctx, "my-func", ver.Version)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.RequestedProvisionedConcurrentExecutions != 5 {
		t.Fatalf("Get requested = %d, want 5", got.RequestedProvisionedConcurrentExecutions)
	}

	list, err := m.ListFunctionProvisionedConcurrencyConfigs(ctx, "my-func")
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v (err %v), want 1 config", list, err)
	}

	if err := m.DeleteFunctionProvisionedConcurrencyConfig(ctx, "my-func", ver.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := m.GetFunctionProvisionedConcurrencyConfig(ctx, "my-func", ver.Version); err == nil {
		t.Fatal("Get after Delete: want NotFound")
	}
}

func TestProvisionedConcurrencyRejectsLatest(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	for _, qualifier := range []string{"", latestVersion} {
		_, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
			FunctionName:                             "my-func",
			Qualifier:                                qualifier,
			RequestedProvisionedConcurrentExecutions: 1,
		})
		if err == nil {
			t.Fatalf("qualifier %q: want InvalidArgument for $LATEST/unqualified", qualifier)
		}
	}
}

func TestProvisionedConcurrencyRejectsUnknownQualifier(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                "99",
		RequestedProvisionedConcurrentExecutions: 1,
	}); err == nil {
		t.Fatal("unpublished version 99: want NotFound")
	}
}

func TestProvisionedConcurrencyRejectsBelowMinimum(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	ver := createPublishedFunc(t, m)

	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                ver.Version,
		RequestedProvisionedConcurrentExecutions: 0,
	}); err == nil {
		t.Fatal("requested=0: want InvalidArgument")
	}
}

// TestProvisionedConcurrencyBoundByReserved covers the AWS constraint:
// provisioned concurrency cannot exceed the function's reserved concurrency
// once one is configured, since it is carved out of that budget.
func TestProvisionedConcurrencyBoundByReserved(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	ver := createPublishedFunc(t, m)

	if err := m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
		FunctionName:                 "my-func",
		ReservedConcurrentExecutions: 3,
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	// Within the reserved budget: succeeds.
	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                ver.Version,
		RequestedProvisionedConcurrentExecutions: 3,
	}); err != nil {
		t.Fatalf("Put within reserved budget: %v", err)
	}

	if err := m.DeleteFunctionProvisionedConcurrencyConfig(ctx, "my-func", ver.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Exceeding the reserved budget: rejected.
	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                ver.Version,
		RequestedProvisionedConcurrentExecutions: 4,
	}); err == nil {
		t.Fatal("Put over reserved budget: want InvalidArgument")
	}
}

func TestProvisionedConcurrencyUnknownFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "absent",
		Qualifier:                                "1",
		RequestedProvisionedConcurrentExecutions: 1,
	}); err == nil {
		t.Fatal("unknown function Put: want NotFound")
	}

	if _, err := m.GetFunctionProvisionedConcurrencyConfig(ctx, "absent", "1"); err == nil {
		t.Fatal("unknown function Get: want NotFound")
	}

	if err := m.DeleteFunctionProvisionedConcurrencyConfig(ctx, "absent", "1"); err == nil {
		t.Fatal("unknown function Delete: want NotFound")
	}

	if _, err := m.ListFunctionProvisionedConcurrencyConfigs(ctx, "absent"); err == nil {
		t.Fatal("unknown function List: want NotFound")
	}
}

// TestProvisionedConcurrencyCOWIndependence proves setProvisionedConcurrencyConfig's
// copy-on-write map means a concurrent reader holding an earlier funcData copy
// never observes a partially written map -- guards -race cleanliness alongside
// the concurrent writers below.
func TestProvisionedConcurrencyCOWIndependence(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	vers := make([]*driver.FunctionVersion, 0, 3)

	for i := 0; i < 3; i++ {
		ver, err := m.PublishVersion(ctx, "my-func", "")
		if err != nil {
			t.Fatalf("PublishVersion: %v", err)
		}

		vers = append(vers, ver)
	}

	var wg sync.WaitGroup

	wg.Add(len(vers))

	for _, ver := range vers {
		go func(qualifier string) {
			defer wg.Done()

			if _, err := m.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
				FunctionName:                             "my-func",
				Qualifier:                                qualifier,
				RequestedProvisionedConcurrentExecutions: 1,
			}); err != nil {
				t.Errorf("concurrent Put(%s): %v", qualifier, err)
			}
		}(ver.Version)
	}

	wg.Wait()

	list, err := m.ListFunctionProvisionedConcurrencyConfigs(ctx, "my-func")
	if err != nil || len(list) != len(vers) {
		t.Fatalf("List = %v (err %v), want %d configs", list, err, len(vers))
	}
}
