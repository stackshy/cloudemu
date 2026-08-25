package eventgrid_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKDomainTopicsLifecycle is the HIGH regression: DomainTopicsClient
// CRUD against a real domain must work end to end (create, get, list,
// delete) instead of 400ing.
func TestSDKDomainTopicsLifecycle(t *testing.T) {
	cf, _ := newEGFactory(t)
	ctx := context.Background()

	domains := cf.NewDomainsClient()
	domainPoller, err := domains.BeginCreateOrUpdate(ctx, testRG, "shop-domain", armeventgrid.Domain{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Domains.BeginCreateOrUpdate: %v", err)
	}

	if _, err := domainPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Domains PollUntilDone: %v", err)
	}

	domainTopics := cf.NewDomainTopicsClient()

	topicPoller, err := domainTopics.BeginCreateOrUpdate(ctx, testRG, "shop-domain", "orders", nil)
	if err != nil {
		t.Fatalf("DomainTopics.BeginCreateOrUpdate: %v", err)
	}

	created, err := topicPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DomainTopics PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "orders" {
		t.Fatalf("created name = %v, want orders", created.Name)
	}

	got, err := domainTopics.Get(ctx, testRG, "shop-domain", "orders", nil)
	if err != nil {
		t.Fatalf("DomainTopics.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.ProvisioningState == nil ||
		*got.Properties.ProvisioningState != armeventgrid.DomainTopicProvisioningStateSucceeded {
		t.Fatalf("provisioningState = %v, want Succeeded", got.Properties)
	}

	var names []string

	pager := domainTopics.NewListByDomainPager(testRG, "shop-domain", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByDomain: %v", perr)
		}

		for _, dt := range page.Value {
			names = append(names, *dt.Name)
		}
	}

	if len(names) != 1 || names[0] != "orders" {
		t.Fatalf("list = %v, want [orders]", names)
	}

	delPoller, err := domainTopics.BeginDelete(ctx, testRG, "shop-domain", "orders", nil)
	if err != nil {
		t.Fatalf("DomainTopics.BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("DomainTopics delete PollUntilDone: %v", err)
	}

	if _, err := domainTopics.Get(ctx, testRG, "shop-domain", "orders", nil); err == nil {
		t.Fatal("Get after delete: expected error, got nil")
	}
}

// TestSDKDomainTopicOnMissingDomain locks the ParentResourceNotFound
// semantics: a domain topic created under a nonexistent domain must fail,
// not silently succeed.
func TestSDKDomainTopicOnMissingDomain(t *testing.T) {
	cf, _ := newEGFactory(t)
	ctx := context.Background()

	domainTopics := cf.NewDomainTopicsClient()

	_, err := domainTopics.BeginCreateOrUpdate(ctx, testRG, "no-such-domain", "orders", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("BeginCreateOrUpdate on missing domain: got %v, want 404", err)
	}
}
