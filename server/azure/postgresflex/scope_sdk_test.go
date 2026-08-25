package postgresflex_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

// mustCreateServerInRG creates a bare server under the given resource group.
func mustCreateServerInRG(t *testing.T, servers *armpostgresqlflexibleservers.ServersClient, rg, name string) {
	t.Helper()

	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, rg, name, armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate %s/%s: %v", rg, name, err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone %s/%s: %v", rg, name, err)
	}
}

// TestSDKPostgresFlexScopedListing asserts a server created in one resource
// group must not appear when listing a different resource group.
func TestSDKPostgresFlexScopedListing(t *testing.T) {
	servers := newSDKClient(t)
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a1")
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a2")
	mustCreateServerInRG(t, servers, "rg-team-b", "srv-b1")

	ctx := context.Background()

	listRG := func(rg string) []string {
		var names []string

		pager := servers.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}

			for _, s := range page.Value {
				names = append(names, *s.Name)
			}
		}

		return names
	}

	gotA := listRG("rg-team-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-team-a listed %v, want exactly its own 2 servers", gotA)
	}

	gotB := listRG("rg-team-b")
	if len(gotB) != 1 || gotB[0] != "srv-b1" {
		t.Fatalf("rg-team-b listed %v, want [srv-b1]", gotB)
	}
}

// TestSDKPostgresFlexScopedGetNotFound asserts a server created under one
// resource group cannot be resolved via Get under a different resource group
// (real ARM answers 404, since the id would contradict the request path).
func TestSDKPostgresFlexScopedGetNotFound(t *testing.T) {
	servers := newSDKClient(t)
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a1")

	ctx := context.Background()

	if _, err := servers.Get(ctx, "rg-team-a", "srv-a1", nil); err != nil {
		t.Fatalf("Get in owning resource group: %v", err)
	}

	if _, err := servers.Get(ctx, "rg-team-b", "srv-a1", nil); err == nil {
		t.Fatal("Get from a different resource group: expected NotFound, got nil")
	}
}

// TestSDKPostgresFlexScopedDeleteNotFound asserts a DELETE issued under the
// wrong resource group cannot remove another resource group's server — the
// cross-tenant leak this behavior guards against.
func TestSDKPostgresFlexScopedDeleteNotFound(t *testing.T) {
	servers := newSDKClient(t)
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a1")

	ctx := context.Background()

	poller, err := servers.BeginDelete(ctx, "rg-team-b", "srv-a1", nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("Delete from a different resource group: expected an error, got nil")
	}

	// The server must still exist, untouched, under its real resource group.
	if _, err := servers.Get(ctx, "rg-team-a", "srv-a1", nil); err != nil {
		t.Fatalf("Get after cross-group delete attempt: %v", err)
	}
}
