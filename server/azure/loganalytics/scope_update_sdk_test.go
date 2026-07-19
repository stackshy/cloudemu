package loganalytics_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"
)

func createWS(t *testing.T, client *armoperationalinsights.WorkspacesClient, rg, name string, tags map[string]*string) armoperationalinsights.Workspace {
	t.Helper()
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armoperationalinsights.Workspace{
		Location: to.Ptr("eastus"),
		Tags:     tags,
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s/%s: %v", rg, name, err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll %s/%s: %v", rg, name, err)
	}
	return res.Workspace
}

// TestSDKScopedListing asserts #259 item 1 through the real SDK: workspaces
// created in one resource group must not appear in another group's list.
func TestSDKScopedListing(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	createWS(t, client, "rg-team-a", "ws-a1", nil)
	createWS(t, client, "rg-team-a", "ws-a2", nil)
	createWS(t, client, "rg-team-b", "ws-b1", nil)

	listRG := func(rg string) []string {
		var names []string
		pager := client.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, ws := range page.Value {
				names = append(names, *ws.Name)
			}
		}
		return names
	}

	gotA := listRG("rg-team-a")
	if len(gotA) != 2 {
		t.Fatalf("rg-team-a listed %v, want exactly its own 2 workspaces", gotA)
	}
	gotB := listRG("rg-team-b")
	if len(gotB) != 1 || gotB[0] != "ws-b1" {
		t.Fatalf("rg-team-b listed %v, want [ws-b1]", gotB)
	}
}

// TestSDKUpsertAppliesUpdates asserts #259 item 2 through the real SDK:
// CreateOrUpdate on an existing workspace must apply the request's tags,
// not echo the stale resource.
func TestSDKUpsertAppliesUpdates(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	createWS(t, client, testRG, "ws-upsert", map[string]*string{"env": to.Ptr("dev")})

	updated := createWS(t, client, testRG, "ws-upsert",
		map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("core")})
	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("upsert response tags = %v, want env=prod applied", updated.Tags)
	}

	got, err := client.Get(ctx, testRG, "ws-upsert", nil)
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
	client := newWorkspacesClient(t)

	ws := createWS(t, client, "rg-id-check", "ws-id", nil)
	if ws.ID == nil {
		t.Fatal("workspace response has no id")
	}
	id := *ws.ID
	if !strings.Contains(id, "/subscriptions/"+testSub+"/") || !strings.Contains(id, "/resourceGroups/rg-id-check/") {
		t.Fatalf("id = %q, want it under subscription %q and resource group rg-id-check", id, testSub)
	}
}
