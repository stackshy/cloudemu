package eventhub_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
)

// TestSDKNamespaceSKUCapacityDefault checks that a namespace created without an
// explicit sku.capacity reports capacity 1, matching real Azure (which always
// returns a capacity and defaults it to 1). An omitted capacity causes read-back
// drift for SDK/CLI/Terraform.
func TestSDKNamespaceSKUCapacityDefault(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	c, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewNamespacesClient: %v", err)
	}

	cases := []struct {
		name string
		sku  *armeventhub.SKU
	}{
		{"standard-no-capacity", &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUNameStandard)}},
		{"no-sku-at-all", nil},
	}

	for i, tc := range cases {
		ns := nsName + "-cap"
		if i == 1 {
			ns += "2"
		}

		poller, err := c.BeginCreateOrUpdate(ctx, rgName, ns, armeventhub.EHNamespace{
			Location: to.Ptr("eastus"),
			SKU:      tc.sku,
		}, nil)
		if err != nil {
			t.Fatalf("%s: BeginCreateOrUpdate: %v", tc.name, err)
		}

		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("%s: poll create: %v", tc.name, err)
		}

		got, err := c.Get(ctx, rgName, ns, nil)
		if err != nil {
			t.Fatalf("%s: Get: %v", tc.name, err)
		}

		if got.SKU == nil || got.SKU.Capacity == nil {
			t.Fatalf("%s: sku.capacity = nil, want 1", tc.name)
		}

		if *got.SKU.Capacity != 1 {
			t.Fatalf("%s: sku.capacity = %d, want 1", tc.name, *got.SKU.Capacity)
		}
	}
}

// TestSDKDefaultConsumerGroupNotDeletable checks that the built-in $Default
// consumer group cannot be deleted, matching real Azure (which rejects the
// request). This is a well-known Terraform pain point.
func TestSDKDefaultConsumerGroupNotDeletable(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	seedEventHub(t, ctx, ts)

	cgClient, err := armeventhub.NewConsumerGroupsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewConsumerGroupsClient: %v", err)
	}

	_, err = cgClient.Delete(ctx, rgName, nsName, ehName, "$Default", nil)
	if err == nil {
		t.Fatal("Delete $Default consumer group returned nil error, want BadRequest")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("Delete $Default error = %v, want HTTP 400", err)
	}

	// $Default must still be present after the rejected delete.
	if _, err := cgClient.Get(ctx, rgName, nsName, ehName, "$Default", nil); err != nil {
		t.Fatalf("$Default consumer group Get after rejected delete: %v", err)
	}

	// A non-default consumer group is still deletable.
	if _, err := cgClient.CreateOrUpdate(ctx, rgName, nsName, ehName, "workers",
		armeventhub.ConsumerGroup{}, nil); err != nil {
		t.Fatalf("CreateOrUpdate workers consumer group: %v", err)
	}

	if _, err := cgClient.Delete(ctx, rgName, nsName, ehName, "workers", nil); err != nil {
		t.Fatalf("Delete workers consumer group: %v", err)
	}
}

// TestSDKEventHubPropertyValidation checks that out-of-range partitionCount and
// messageRetentionInDays are rejected on a Standard-tier namespace, matching real
// Azure's BadRequest, while in-range values succeed.
func TestSDKEventHubPropertyValidation(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	createStandardNamespace(t, ctx, ts)

	ehClient, err := armeventhub.NewEventHubsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewEventHubsClient: %v", err)
	}

	bad := []struct {
		name  string
		props *armeventhub.Properties
	}{
		{"partition-count-too-high", &armeventhub.Properties{PartitionCount: to.Ptr[int64](100)}},
		{"partition-count-zero", &armeventhub.Properties{PartitionCount: to.Ptr[int64](0)}},
		{"retention-too-high", &armeventhub.Properties{MessageRetentionInDays: to.Ptr[int64](30)}},
	}

	for _, tc := range bad {
		_, err := ehClient.CreateOrUpdate(ctx, rgName, nsName, "eh-"+tc.name,
			armeventhub.Eventhub{Properties: tc.props}, nil)
		if err == nil {
			t.Fatalf("%s: CreateOrUpdate returned nil error, want BadRequest", tc.name)
		}

		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: error = %v, want HTTP 400", tc.name, err)
		}
	}

	// In-range values on Standard succeed (32 partitions, 7-day retention).
	if _, err := ehClient.CreateOrUpdate(ctx, rgName, nsName, "eh-valid", armeventhub.Eventhub{
		Properties: &armeventhub.Properties{
			PartitionCount:         to.Ptr[int64](32),
			MessageRetentionInDays: to.Ptr[int64](7),
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate valid event hub: %v", err)
	}
}

func createStandardNamespace(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	c, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewNamespacesClient: %v", err)
	}

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, nsName, armeventhub.EHNamespace{
		Location: to.Ptr("eastus"),
		SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUNameStandard)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate namespace: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll namespace create: %v", err)
	}
}

func seedEventHub(t *testing.T, ctx context.Context, ts *httptest.Server) {
	t.Helper()

	createStandardNamespace(t, ctx, ts)

	ehClient, err := armeventhub.NewEventHubsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewEventHubsClient: %v", err)
	}

	if _, err := ehClient.CreateOrUpdate(ctx, rgName, nsName, ehName, armeventhub.Eventhub{
		Properties: &armeventhub.Properties{PartitionCount: to.Ptr[int64](4)},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate event hub: %v", err)
	}
}
