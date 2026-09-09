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

// TestSDKNamespaceUpdateAppliesProperties checks that the ARM Namespaces - Update
// (PATCH) operation applies the properties in the request body as a partial
// update, matching real Azure. A caller that PATCHes isAutoInflateEnabled and
// maximumThroughputUnits must read those values back; silently dropping them
// causes read-back drift for the SDK, the Azure CLI (az eventhubs namespace
// update) and any tool that uses PATCH rather than PUT.
func TestSDKNamespaceUpdateAppliesProperties(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	c, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewNamespacesClient: %v", err)
	}

	createStandardNamespace(t, ctx, ts)

	// PATCH: turn AutoInflate on and set a maximum throughput unit ceiling.
	updated, err := c.Update(ctx, rgName, nsName, armeventhub.EHNamespace{
		Properties: &armeventhub.EHNamespaceProperties{
			IsAutoInflateEnabled:   to.Ptr(true),
			MaximumThroughputUnits: to.Ptr[int32](10),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Update namespace: %v", err)
	}

	if updated.Properties == nil || updated.Properties.IsAutoInflateEnabled == nil ||
		!*updated.Properties.IsAutoInflateEnabled {
		t.Fatalf("PATCH response isAutoInflateEnabled = %v, want true", updated.Properties)
	}

	got, err := c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if got.Properties.IsAutoInflateEnabled == nil || !*got.Properties.IsAutoInflateEnabled {
		t.Fatalf("read-back isAutoInflateEnabled = %v, want true", got.Properties.IsAutoInflateEnabled)
	}

	if got.Properties.MaximumThroughputUnits == nil || *got.Properties.MaximumThroughputUnits != 10 {
		t.Fatalf("read-back maximumThroughputUnits = %v, want 10", got.Properties.MaximumThroughputUnits)
	}

	// A PATCH that omits a property must leave it unchanged (partial update): a
	// tags-only update must not reset isAutoInflateEnabled to false.
	if _, err := c.Update(ctx, rgName, nsName, armeventhub.EHNamespace{
		Tags: map[string]*string{"team": to.Ptr("data")},
	}, nil); err != nil {
		t.Fatalf("tags-only Update: %v", err)
	}

	got, err = c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get after tags-only update: %v", err)
	}

	if got.Properties.IsAutoInflateEnabled == nil || !*got.Properties.IsAutoInflateEnabled {
		t.Fatalf("isAutoInflateEnabled after tags-only PATCH = %v, want it preserved as true",
			got.Properties.IsAutoInflateEnabled)
	}
}

// TestSDKNamespaceUpdateAppliesExplicitFalse checks that a PATCH which flips a
// boolean property from true to an EXPLICIT false (and a numeric property from a
// non-zero value to 0) is actually applied — the change-to-default case a naive
// echo-of-request cannot handle. Real Azure's Namespaces - Update applies the
// values the caller sends, so isAutoInflateEnabled=false and
// maximumThroughputUnits=0 must be read back after the PATCH, not the stale
// creation-time values. The SDK sends *bool/*int32 fields, so a pointer to
// false/0 is serialized on the wire (omitempty only drops a nil pointer), making
// the explicit-false intent observable to the server.
func TestSDKNamespaceUpdateAppliesExplicitFalse(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	c, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewNamespacesClient: %v", err)
	}

	// Create the namespace with AutoInflate ON, a non-zero throughput ceiling and
	// zoneRedundant/disableLocalAuth ON, so the PATCH below has true values to
	// clear rather than fields that were never set.
	poller, err := c.BeginCreateOrUpdate(ctx, rgName, nsName, armeventhub.EHNamespace{
		Location: to.Ptr("eastus"),
		SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUNameStandard)},
		Properties: &armeventhub.EHNamespaceProperties{
			IsAutoInflateEnabled:   to.Ptr(true),
			MaximumThroughputUnits: to.Ptr[int32](10),
			ZoneRedundant:          to.Ptr(true),
			DisableLocalAuth:       to.Ptr(true),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate namespace: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll namespace create: %v", err)
	}

	// Sanity: the creation values are present before the clearing PATCH.
	got, err := c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}

	if got.Properties.IsAutoInflateEnabled == nil || !*got.Properties.IsAutoInflateEnabled ||
		got.Properties.MaximumThroughputUnits == nil || *got.Properties.MaximumThroughputUnits != 10 {
		t.Fatalf("pre-PATCH read-back = autoInflate:%v maxTU:%v, want true/10",
			got.Properties.IsAutoInflateEnabled, got.Properties.MaximumThroughputUnits)
	}

	// PATCH the booleans to EXPLICIT false and the ceiling to 0. Because these are
	// scalar zero values the request-echo overlay deliberately does not capture
	// (it treats a zero scalar the response omits as a modeled default, matching
	// the PATCH-to-clear guard), so read-back can only return them if the handler
	// itself merged the request properties onto the stored state.
	if _, err := c.Update(ctx, rgName, nsName, armeventhub.EHNamespace{
		Properties: &armeventhub.EHNamespaceProperties{
			IsAutoInflateEnabled:   to.Ptr(false),
			MaximumThroughputUnits: to.Ptr[int32](0),
			ZoneRedundant:          to.Ptr(false),
			DisableLocalAuth:       to.Ptr(false),
		},
	}, nil); err != nil {
		t.Fatalf("Update namespace (clear): %v", err)
	}

	got, err = c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get after clearing update: %v", err)
	}

	if got.Properties.IsAutoInflateEnabled == nil || *got.Properties.IsAutoInflateEnabled {
		t.Fatalf("read-back isAutoInflateEnabled = %v, want false (explicit-false applied)",
			got.Properties.IsAutoInflateEnabled)
	}

	if got.Properties.MaximumThroughputUnits == nil || *got.Properties.MaximumThroughputUnits != 0 {
		t.Fatalf("read-back maximumThroughputUnits = %v, want 0 (explicit-zero applied)",
			got.Properties.MaximumThroughputUnits)
	}

	if got.Properties.ZoneRedundant == nil || *got.Properties.ZoneRedundant {
		t.Fatalf("read-back zoneRedundant = %v, want false", got.Properties.ZoneRedundant)
	}

	if got.Properties.DisableLocalAuth == nil || *got.Properties.DisableLocalAuth {
		t.Fatalf("read-back disableLocalAuth = %v, want false", got.Properties.DisableLocalAuth)
	}

	// A later tags-only PATCH omits every property; the just-cleared false must be
	// preserved (omitted-field partial-update merge), not resurrected to its
	// creation-time true.
	if _, err := c.Update(ctx, rgName, nsName, armeventhub.EHNamespace{
		Tags: map[string]*string{"team": to.Ptr("data")},
	}, nil); err != nil {
		t.Fatalf("tags-only Update: %v", err)
	}

	got, err = c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get after tags-only update: %v", err)
	}

	if got.Properties.IsAutoInflateEnabled == nil || *got.Properties.IsAutoInflateEnabled {
		t.Fatalf("isAutoInflateEnabled after tags-only PATCH = %v, want it preserved as false",
			got.Properties.IsAutoInflateEnabled)
	}

	if got.Properties.MaximumThroughputUnits == nil || *got.Properties.MaximumThroughputUnits != 0 {
		t.Fatalf("maximumThroughputUnits after tags-only PATCH = %v, want it preserved as 0",
			got.Properties.MaximumThroughputUnits)
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
