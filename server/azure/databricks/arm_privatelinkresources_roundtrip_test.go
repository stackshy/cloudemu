package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// newPLRoundtripClient builds a PrivateLinkResourcesClient wired to the emulator.
func newPLRoundtripClient(t *testing.T) *armdatabricks.PrivateLinkResourcesClient {
	t.Helper()

	opts, sub := newARMOptions(t)

	client, err := armdatabricks.NewPrivateLinkResourcesClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

func TestSDKPrivateLinkResourceList(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewPrivateLinkResourcesClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()

	pager := client.NewListPager(testRG, testWS, nil)

	var got []*armdatabricks.GroupIDInformation

	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("NextPage: %v", perr)
		}

		got = append(got, page.Value...)
	}

	if len(got) != 2 {
		t.Fatalf("got %d private link resources, want 2", len(got))
	}

	seen := map[string]*armdatabricks.GroupIDInformation{}

	for _, g := range got {
		if g.Properties == nil || g.Properties.GroupID == nil {
			t.Fatalf("resource %+v missing GroupID", g)
		}

		seen[*g.Properties.GroupID] = g
	}

	for _, want := range []string{"databricks_ui_api", "browser_authentication"} {
		g, ok := seen[want]
		if !ok {
			t.Fatalf("group id %q not present in list; got %v", want, plrGroupKeys(seen))
		}

		if len(g.Properties.RequiredMembers) == 0 {
			t.Fatalf("group id %q has empty RequiredMembers", want)
		}

		if len(g.Properties.RequiredZoneNames) == 0 {
			t.Fatalf("group id %q has empty RequiredZoneNames", want)
		}
	}
}

func TestSDKPrivateLinkResourceGet(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewPrivateLinkResourcesClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Get(context.Background(), testRG, testWS, "databricks_ui_api", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	g := resp.GroupIDInformation

	if g.Name == nil || *g.Name != "databricks_ui_api" {
		t.Fatalf("got name %v, want databricks_ui_api", g.Name)
	}

	if g.Properties == nil || g.Properties.GroupID == nil || *g.Properties.GroupID != "databricks_ui_api" {
		t.Fatalf("got group id %+v, want databricks_ui_api", g.Properties)
	}

	if len(g.Properties.RequiredZoneNames) == 0 {
		t.Fatal("expected RequiredZoneNames to be present")
	}

	if *g.Properties.RequiredZoneNames[0] != "privatelink.azuredatabricks.net" {
		t.Fatalf("got zone %q, want privatelink.azuredatabricks.net", *g.Properties.RequiredZoneNames[0])
	}
}

func TestSDKPrivateLinkResourceGetUnknown(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewPrivateLinkResourcesClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.Get(context.Background(), testRG, testWS, "nope", nil); err == nil {
		t.Fatal("expected error for unknown group id")
	}
}

func TestSDKPrivateLinkResourceMissingWorkspace(t *testing.T) {
	client := newPLRoundtripClient(t)
	ctx := context.Background()

	if _, err := client.Get(ctx, testRG, "does-not-exist", "databricks_ui_api", nil); err == nil {
		t.Fatal("expected error getting private link resource on missing workspace")
	}

	pager := client.NewListPager(testRG, "does-not-exist", nil)

	if _, err := pager.NextPage(ctx); err == nil {
		t.Fatal("expected error listing private link resources on missing workspace")
	}
}

// plrGroupKeys returns the map keys, used for readable failure messages.
func plrGroupKeys(m map[string]*armdatabricks.GroupIDInformation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
