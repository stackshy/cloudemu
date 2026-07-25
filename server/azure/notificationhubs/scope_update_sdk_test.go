package notificationhubs_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"
)

func createNS(t *testing.T, client *armnotificationhubs.NamespacesClient, rg, name string, tags map[string]*string) armnotificationhubs.NamespaceResource {
	t.Helper()
	ctx := context.Background()

	res, err := client.CreateOrUpdate(ctx, rg, name, armnotificationhubs.NamespaceCreateOrUpdateParameters{
		Location: to.Ptr("global"),
		Tags:     tags,
	}, nil)
	if err != nil {
		t.Fatalf("Namespaces.CreateOrUpdate %s/%s: %v", rg, name, err)
	}

	return res.NamespaceResource
}

// TestSDKScopedListing asserts #259 item 1 through the real SDK: namespaces
// created in one resource group must not appear in another group's list.
func TestSDKScopedListing(t *testing.T) {
	namespaces, _ := newClients(t)
	ctx := context.Background()

	createNS(t, namespaces, "rg-team-a", "ns-a1", nil)
	createNS(t, namespaces, "rg-team-a", "ns-a2", nil)
	createNS(t, namespaces, "rg-team-b", "ns-b1", nil)

	listRG := func(rg string) []string {
		var names []string
		pager := namespaces.NewListPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, ns := range page.Value {
				names = append(names, *ns.Name)
			}
		}
		return names
	}

	gotA := listRG("rg-team-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-team-a listed %v, want exactly its own 2 namespaces", gotA)
	}
	gotB := listRG("rg-team-b")
	if len(gotB) != 1 || gotB[0] != "ns-b1" {
		t.Fatalf("rg-team-b listed %v, want [ns-b1]", gotB)
	}
}

// TestSDKUpsertAppliesUpdates asserts #259 item 2 through the real SDK:
// CreateOrUpdate on an existing namespace must apply the request's tags,
// not echo the stale resource.
func TestSDKUpsertAppliesUpdates(t *testing.T) {
	namespaces, _ := newClients(t)
	ctx := context.Background()

	createNS(t, namespaces, testRG, "ns-upsert", map[string]*string{"env": to.Ptr("dev")})

	updated := createNS(t, namespaces, testRG, "ns-upsert",
		map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("core")})
	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("upsert response tags = %v, want env=prod applied", updated.Tags)
	}

	got, err := namespaces.Get(ctx, testRG, "ns-upsert", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" || got.Tags["team"] == nil {
		t.Fatalf("stored tags = %v, want env=prod team=core (CreateOrUpdate must not discard updates)", got.Tags)
	}
}

// TestSDKResourceIDMatchesRequestScope asserts #259 item 4 through the real
// SDK: the returned ARM id carries the request's subscription and resource
// group, not a hardcoded default.
func TestSDKResourceIDMatchesRequestScope(t *testing.T) {
	namespaces, _ := newClients(t)

	ns := createNS(t, namespaces, "rg-id-check", "ns-id", nil)
	if ns.ID == nil {
		t.Fatal("namespace response has no id")
	}
	id := *ns.ID
	if !strings.Contains(id, "/subscriptions/"+testSub+"/") || !strings.Contains(id, "/resourceGroups/rg-id-check/") {
		t.Fatalf("id = %q, want it under subscription %q and resource group rg-id-check", id, testSub)
	}
}
