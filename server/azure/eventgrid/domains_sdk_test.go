package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKDomainLifecycle drives the DomainsClient: CreateOrUpdate → Get →
// ListSharedAccessKeys → RegenerateKey (key rotates) → list → Delete.
func TestSDKDomainLifecycle(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()
	ctx := context.Background()

	poller, err := dc.BeginCreateOrUpdate(ctx, testRG, "events-domain", armeventgrid.Domain{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("Domains.BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Domains create PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.Endpoint == nil || *created.Properties.Endpoint == "" {
		t.Fatalf("domain endpoint missing: %+v", created.Properties)
	}

	got, err := dc.Get(ctx, testRG, "events-domain", nil)
	if err != nil {
		t.Fatalf("Domains.Get: %v", err)
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "data" {
		t.Fatalf("tags round-trip failed: %+v", got.Tags)
	}

	// Keys are present and stable across reads.
	keys1, err := dc.ListSharedAccessKeys(ctx, testRG, "events-domain", nil)
	if err != nil {
		t.Fatalf("ListSharedAccessKeys: %v", err)
	}

	if keys1.Key1 == nil || keys1.Key2 == nil || *keys1.Key1 == "" || *keys1.Key2 == "" {
		t.Fatalf("shared access keys empty: %+v", keys1)
	}

	keys1b, err := dc.ListSharedAccessKeys(ctx, testRG, "events-domain", nil)
	if err != nil {
		t.Fatalf("ListSharedAccessKeys second: %v", err)
	}

	if *keys1b.Key1 != *keys1.Key1 {
		t.Fatalf("key1 changed between reads: %q vs %q", *keys1b.Key1, *keys1.Key1)
	}

	// Regenerate key1: it must change, key2 must not.
	regen, err := dc.RegenerateKey(ctx, testRG, "events-domain", armeventgrid.DomainRegenerateKeyRequest{
		KeyName: to.Ptr("key1"),
	}, nil)
	if err != nil {
		t.Fatalf("RegenerateKey: %v", err)
	}

	if *regen.Key1 == *keys1.Key1 {
		t.Fatalf("key1 did not change after regenerate: %q", *regen.Key1)
	}

	if *regen.Key2 != *keys1.Key2 {
		t.Fatalf("key2 changed on key1 regenerate: %q vs %q", *regen.Key2, *keys1.Key2)
	}

	// List by subscription includes the domain.
	var names []string

	pager := dc.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListBySubscription: %v", perr)
		}

		for _, d := range page.Value {
			names = append(names, *d.Name)
		}
	}

	if len(names) != 1 || names[0] != "events-domain" {
		t.Fatalf("ListBySubscription = %v, want [events-domain]", names)
	}

	delPoller, err := dc.BeginDelete(ctx, testRG, "events-domain", nil)
	if err != nil {
		t.Fatalf("Domains.BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Domains delete PollUntilDone: %v", err)
	}

	if _, err := dc.Get(ctx, testRG, "events-domain", nil); err == nil {
		t.Fatal("Get after delete: expected error, got nil")
	}
}

// TestSDKDomainGetMissing asserts a Get on an absent domain 404s.
func TestSDKDomainGetMissing(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()

	if _, err := dc.Get(context.Background(), testRG, "ghost", nil); err == nil {
		t.Fatal("Get missing domain: expected error, got nil")
	}
}
