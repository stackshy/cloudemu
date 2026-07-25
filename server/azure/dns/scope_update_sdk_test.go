package dns_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
)

// TestSDKScopedListing asserts through the real SDK that zones created in one
// resource group do not appear in another group's ListByResourceGroup pager.
func TestSDKScopedListing(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	create := func(rg, name string) {
		if _, err := zones.CreateOrUpdate(ctx, rg, name, armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
			t.Fatalf("CreateOrUpdate %s/%s: %v", rg, name, err)
		}
	}
	create("rg-team-a", "a1.com")
	create("rg-team-a", "a2.com")
	create("rg-team-b", "b1.com")

	listRG := func(rg string) []string {
		var names []string
		pager := zones.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, z := range page.Value {
				names = append(names, *z.Name)
			}
		}
		return names
	}

	gotA := listRG("rg-team-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-team-a listed %v, want exactly its own 2 zones", gotA)
	}
	gotB := listRG("rg-team-b")
	if len(gotB) != 1 || gotB[0] != "b1.com" {
		t.Fatalf("rg-team-b listed %v, want [b1.com]", gotB)
	}
}

// TestSDKUpsertAppliesTags asserts through the real SDK that CreateOrUpdate on
// an existing zone applies the request's tags, not echoing the stale resource.
func TestSDKUpsertAppliesTags(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "upsert.com", armdns.Zone{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate (create): %v", err)
	}

	updated, err := zones.CreateOrUpdate(ctx, testRG, "upsert.com", armdns.Zone{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("core")},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate (update): %v", err)
	}
	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("upsert response tags = %v, want env=prod applied", updated.Tags)
	}

	got, err := zones.Get(ctx, testRG, "upsert.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" || got.Tags["team"] == nil {
		t.Fatalf("stored tags = %v, want env=prod team=core (CreateOrUpdate must not discard updates)", got.Tags)
	}
}

// TestSDKZoneIDMatchesRequestScope asserts through the real SDK that the
// returned ARM id carries the request's subscription and resource group.
func TestSDKZoneIDMatchesRequestScope(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	created, err := zones.CreateOrUpdate(ctx, "rg-id-check", "id.com", armdns.Zone{
		Location: to.Ptr("global"),
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}
	if created.ID == nil {
		t.Fatal("zone response has no id")
	}
	id := *created.ID
	if !strings.Contains(id, "/subscriptions/"+testSub+"/") || !strings.Contains(id, "/resourceGroups/rg-id-check/") {
		t.Fatalf("id = %q, want it under subscription %q and resource group rg-id-check", id, testSub)
	}
}
