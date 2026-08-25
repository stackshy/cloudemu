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

func skuName(s *armpostgresqlflexibleservers.Server) string {
	if s == nil || s.SKU == nil || s.SKU.Name == nil {
		return ""
	}

	return *s.SKU.Name
}

// TestSDKPostgresFlexScopedUpdateNotFound asserts a PATCH issued under the wrong
// resource group cannot mutate another resource group's server (the SKU/storage
// cross-tenant write) — it must 404 and leave the real server untouched.
func TestSDKPostgresFlexScopedUpdateNotFound(t *testing.T) {
	servers := newSDKClient(t)
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a1")

	ctx := context.Background()

	before, err := servers.Get(ctx, "rg-team-a", "srv-a1", nil)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	origSKU := skuName(&before.Server)

	poller, err := servers.BeginUpdate(ctx, "rg-team-b", "srv-a1", armpostgresqlflexibleservers.ServerForUpdate{
		SKU: &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_D8s_v3")},
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("Update from a different resource group: expected an error, got nil")
	}

	after, err := servers.Get(ctx, "rg-team-a", "srv-a1", nil)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}

	if got := skuName(&after.Server); got != origSKU {
		t.Fatalf("cross-group update mutated the owner's SKU: %q -> %q", origSKU, got)
	}
}

// TestSDKPostgresFlexCrossGroupCreateConflict asserts a same-name PUT from a
// different resource group conflicts (Flex names are globally FQDN-unique)
// rather than silently mutating the real owner's server.
func TestSDKPostgresFlexCrossGroupCreateConflict(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, "rg-team-a", "srv1", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate rg-team-a: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create rg-team-a: %v", err)
	}

	poller2, err := servers.BeginCreate(ctx, "rg-team-b", "srv1", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("westus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_D8s_v3")},
	}, nil)
	if err == nil {
		_, err = poller2.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("cross-group same-name create: expected a conflict, got nil")
	}

	got, err := servers.Get(ctx, "rg-team-a", "srv1", nil)
	if err != nil {
		t.Fatalf("Get rg-team-a after conflict: %v", err)
	}

	if name := skuName(&got.Server); name != "Standard_B1ms" {
		t.Fatalf("cross-group create mutated the owner's SKU, got %q", name)
	}

	if _, err := servers.Get(ctx, "rg-team-b", "srv1", nil); err == nil {
		t.Fatal("server must not exist under rg-team-b after a rejected create")
	}
}

// TestSDKPostgresFlexScopedStartStopNotFound asserts start/stop issued under the
// wrong resource group cannot act on another resource group's server.
func TestSDKPostgresFlexScopedStartStopNotFound(t *testing.T) {
	servers := newSDKClient(t)
	mustCreateServerInRG(t, servers, "rg-team-a", "srv-a1")

	ctx := context.Background()

	stopP, err := servers.BeginStop(ctx, "rg-team-b", "srv-a1", nil)
	if err == nil {
		_, err = stopP.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("Stop from a different resource group: expected an error, got nil")
	}

	startP, err := servers.BeginStart(ctx, "rg-team-b", "srv-a1", nil)
	if err == nil {
		_, err = startP.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("Start from a different resource group: expected an error, got nil")
	}

	if _, err := servers.Get(ctx, "rg-team-a", "srv-a1", nil); err != nil {
		t.Fatalf("Get after cross-group start/stop attempt: %v", err)
	}
}
