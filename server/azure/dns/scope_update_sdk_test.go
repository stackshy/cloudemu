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

// TestSDKSameNameZonesStayIndependent asserts that CreateOrUpdate for a zone
// name that already exists in a different resource group creates a distinct
// zone rather than hijacking the existing one — the same name legitimately
// exists in more than one group.
func TestSDKSameNameZonesStayIndependent(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	mk := func(rg, env string) {
		if _, err := zones.CreateOrUpdate(ctx, rg, "shared.com", armdns.Zone{
			Location: to.Ptr("global"),
			Tags:     map[string]*string{"env": to.Ptr(env)},
		}, nil); err != nil {
			t.Fatalf("CreateOrUpdate %s: %v", rg, err)
		}
	}
	mk("rg-shared-a", "a")
	mk("rg-shared-b", "b")

	// Each group must still own its own shared.com with its own tag; the
	// second create must not have moved or overwritten the first.
	tagInRG := func(rg string) string {
		var out string
		pager := zones.NewListByResourceGroupPager(rg, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("list %s: %v", rg, err)
			}
			for _, z := range page.Value {
				if z.Name != nil && *z.Name == "shared.com" && z.Tags["env"] != nil {
					out = *z.Tags["env"]
				}
			}
		}
		return out
	}
	if got := tagInRG("rg-shared-a"); got != "a" {
		t.Fatalf("rg-shared-a shared.com env = %q, want a (not hijacked by the rg-b create)", got)
	}
	if got := tagInRG("rg-shared-b"); got != "b" {
		t.Fatalf("rg-shared-b shared.com env = %q, want b", got)
	}
}

// TestSDKTXTRecordChunking asserts that a TXT value longer than 255 bytes is
// returned as valid ≤255-byte character-strings whose concatenation preserves
// the original value — Azure rejects a single oversized chunk.
func TestSDKTXTRecordChunking(t *testing.T) {
	zones, records := newDNSClients(t)
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, testRG, "txt.com", armdns.Zone{Location: to.Ptr("global")}, nil); err != nil {
		t.Fatalf("Zones.CreateOrUpdate: %v", err)
	}

	chunkA := strings.Repeat("a", 255)
	chunkB := strings.Repeat("b", 100)
	want := chunkA + chunkB // 355-byte logical value

	if _, err := records.CreateOrUpdate(ctx, testRG, "txt.com", "long", armdns.RecordTypeTXT, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:        to.Ptr(int64(300)),
			TxtRecords: []*armdns.TxtRecord{{Value: []*string{to.Ptr(chunkA), to.Ptr(chunkB)}}},
		},
	}, nil); err != nil {
		t.Fatalf("RecordSets.CreateOrUpdate: %v", err)
	}

	got, err := records.Get(ctx, testRG, "txt.com", "long", armdns.RecordTypeTXT, nil)
	if err != nil {
		t.Fatalf("RecordSets.Get: %v", err)
	}
	if got.Properties == nil || len(got.Properties.TxtRecords) != 1 {
		t.Fatalf("TxtRecords = %+v, want exactly one record", got.Properties)
	}

	var joined string
	for _, c := range got.Properties.TxtRecords[0].Value {
		if c == nil {
			t.Fatal("nil TXT chunk")
		}
		if len(*c) > 255 {
			t.Fatalf("TXT chunk length %d exceeds the 255-byte limit", len(*c))
		}
		joined += *c
	}
	if joined != want {
		t.Fatalf("reassembled TXT = %d bytes, want the original %d-byte value", len(joined), len(want))
	}
}

// TestSDKGetResolvesWithinRequestScope asserts that when the same zone name
// exists in two resource groups, a Get resolves to the zone in the request's
// group — not an arbitrary same-named zone in another group.
func TestSDKGetResolvesWithinRequestScope(t *testing.T) {
	zones, _ := newDNSClients(t)
	ctx := context.Background()

	mk := func(rg, env string) {
		if _, err := zones.CreateOrUpdate(ctx, rg, "dup.com", armdns.Zone{
			Location: to.Ptr("global"),
			Tags:     map[string]*string{"env": to.Ptr(env)},
		}, nil); err != nil {
			t.Fatalf("CreateOrUpdate %s: %v", rg, err)
		}
	}
	mk("rg-get-a", "a")
	mk("rg-get-b", "b")

	getEnv := func(rg string) string {
		z, err := zones.Get(ctx, rg, "dup.com", nil)
		if err != nil {
			t.Fatalf("Get %s: %v", rg, err)
		}
		if z.Tags["env"] == nil {
			t.Fatalf("Get %s: no env tag", rg)
		}
		return *z.Tags["env"]
	}
	if got := getEnv("rg-get-a"); got != "a" {
		t.Fatalf("Get rg-get-a/dup.com env = %q, want a (must resolve within the request's group)", got)
	}
	if got := getEnv("rg-get-b"); got != "b" {
		t.Fatalf("Get rg-get-b/dup.com env = %q, want b", got)
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
