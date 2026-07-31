package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

const accessConnectorType = "Microsoft.Databricks/accessConnectors"

func newAccessConnectorsClient(t *testing.T) *armdatabricks.AccessConnectorsClient {
	t.Helper()

	opts, sub := newARMOptions(t)

	client, err := armdatabricks.NewAccessConnectorsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

func createAccessConnector(
	t *testing.T, client *armdatabricks.AccessConnectorsClient, name string, ac armdatabricks.AccessConnector,
) armdatabricks.AccessConnector {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, name, ac, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return res.AccessConnector
}

func TestSDKAccessConnectorLifecycle(t *testing.T) {
	client := newAccessConnectorsClient(t)
	ctx := context.Background()

	const name = "conn-1"

	created := createAccessConnector(t, client, name, armdatabricks.AccessConnector{
		Location: to.Ptr("eastus"),
		Identity: &armdatabricks.ManagedServiceIdentity{
			Type: to.Ptr(armdatabricks.ManagedServiceIdentityTypeSystemAssigned),
		},
		Tags: map[string]*string{"env": to.Ptr("test")},
	})

	if created.Name == nil || *created.Name != name {
		t.Fatalf("got name %v, want %q", created.Name, name)
	}

	if created.Type == nil || *created.Type != accessConnectorType {
		t.Fatalf("got type %v, want %q", created.Type, accessConnectorType)
	}

	if created.Properties == nil || created.Properties.ProvisioningState == nil ||
		*created.Properties.ProvisioningState != armdatabricks.ProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded provisioning state, got %+v", created.Properties)
	}

	if created.Identity == nil {
		t.Fatal("expected a system-assigned identity on create")
	}

	if created.Identity.PrincipalID == nil || *created.Identity.PrincipalID == "" {
		t.Fatalf("expected non-empty principal ID, got %v", created.Identity.PrincipalID)
	}

	if created.Identity.TenantID == nil || *created.Identity.TenantID == "" {
		t.Fatalf("expected non-empty tenant ID, got %v", created.Identity.TenantID)
	}

	got, err := client.Get(ctx, testRG, name, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("expected tag env=test, got %v", got.Tags)
	}

	updatePoller, err := client.BeginUpdate(ctx, testRG, name, armdatabricks.AccessConnectorUpdate{
		Tags: map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := updatePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("expected updated tag env=prod, got %v", updated.Tags)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "data" {
		t.Fatalf("expected updated tag team=data, got %v", updated.Tags)
	}

	byRG := client.NewListByResourceGroupPager(testRG, nil)

	rgCount := 0
	for byRG.More() {
		page, perr := byRG.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByResourceGroup: %v", perr)
		}

		rgCount += len(page.Value)
	}

	if rgCount != 1 {
		t.Fatalf("got %d connectors in RG, want 1", rgCount)
	}

	bySub := client.NewListBySubscriptionPager(nil)

	subCount := 0
	for bySub.More() {
		page, perr := bySub.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListBySubscription: %v", perr)
		}

		subCount += len(page.Value)
	}

	if subCount != 1 {
		t.Fatalf("got %d connectors in subscription, want 1", subCount)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, name, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone: %v", err)
	}

	if _, err = client.Get(ctx, testRG, name, nil); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSDKAccessConnectorGetNotFound(t *testing.T) {
	client := newAccessConnectorsClient(t)

	_, err := client.Get(context.Background(), testRG, "does-not-exist", nil)
	if err == nil {
		t.Fatal("expected error for missing access connector")
	}
}

func TestSDKAccessConnectorEmptyLocation(t *testing.T) {
	client := newAccessConnectorsClient(t)
	ctx := context.Background()

	// Location is required; the emulator rejects it with InvalidArgument -> HTTP 400.
	// The error may surface at BeginCreateOrUpdate or during PollUntilDone.
	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "conn-no-loc", armdatabricks.AccessConnector{
		Identity: &armdatabricks.ManagedServiceIdentity{
			Type: to.Ptr(armdatabricks.ManagedServiceIdentityTypeSystemAssigned),
		},
	}, nil)
	if err != nil {
		return
	}

	if _, err = poller.PollUntilDone(ctx, nil); err == nil {
		t.Fatal("expected error creating connector with empty location")
	}
}

func TestSDKAccessConnectorListByResourceGroup(t *testing.T) {
	client := newAccessConnectorsClient(t)
	ctx := context.Background()

	names := []string{"conn-a", "conn-b"}
	for _, name := range names {
		createAccessConnector(t, client, name, armdatabricks.AccessConnector{
			Location: to.Ptr("eastus"),
		})
	}

	byRG := client.NewListByResourceGroupPager(testRG, nil)

	got := map[string]bool{}
	for byRG.More() {
		page, err := byRG.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByResourceGroup: %v", err)
		}

		for _, ac := range page.Value {
			if ac.Name != nil {
				got[*ac.Name] = true
			}
		}
	}

	if len(got) != len(names) {
		t.Fatalf("got %d connectors in RG, want %d (%v)", len(got), len(names), got)
	}

	for _, name := range names {
		if !got[name] {
			t.Fatalf("expected connector %q in list, got %v", name, got)
		}
	}
}

func TestSDKAccessConnectorNoIdentity(t *testing.T) {
	client := newAccessConnectorsClient(t)
	ctx := context.Background()

	// An identity Type of "None" resolves to no system identity in the provider.
	created := createAccessConnector(t, client, "conn-none", armdatabricks.AccessConnector{
		Location: to.Ptr("eastus"),
		Identity: &armdatabricks.ManagedServiceIdentity{
			Type: to.Ptr(armdatabricks.ManagedServiceIdentityTypeNone),
		},
	})

	if created.Identity != nil {
		t.Fatalf("expected no identity for Type=None, got %+v", created.Identity)
	}

	got, err := client.Get(ctx, testRG, "conn-none", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity != nil {
		t.Fatalf("expected no identity on Get for Type=None, got %+v", got.Identity)
	}
}

// TestSDKAccessConnectorPatchIdentityToNone SDK-round-trips the identity
// transition the unit tests cover only in-process: a PATCH with identity type
// None clears a previously system-assigned identity.
func TestSDKAccessConnectorPatchIdentityToNone(t *testing.T) {
	client := newAccessConnectorsClient(t)
	ctx := context.Background()

	const name = "conn-none"

	created := createAccessConnector(t, client, name, armdatabricks.AccessConnector{
		Location: to.Ptr("eastus"),
		Identity: &armdatabricks.ManagedServiceIdentity{
			Type: to.Ptr(armdatabricks.ManagedServiceIdentityTypeSystemAssigned),
		},
	})

	if created.Identity == nil || created.Identity.PrincipalID == nil || *created.Identity.PrincipalID == "" {
		t.Fatalf("expected a system-assigned identity on create, got %+v", created.Identity)
	}

	poller, err := client.BeginUpdate(ctx, testRG, name, armdatabricks.AccessConnectorUpdate{
		Identity: &armdatabricks.ManagedServiceIdentity{
			Type: to.Ptr(armdatabricks.ManagedServiceIdentityTypeNone),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	updated, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	// None clears the identity: the emulator resolves type None to no identity,
	// so the ARM response omits the identity block.
	if updated.Identity != nil {
		t.Fatalf("expected identity cleared after PATCH None, got %+v", updated.Identity)
	}
}
