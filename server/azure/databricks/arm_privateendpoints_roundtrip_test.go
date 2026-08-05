package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

const wantPECType = "Microsoft.Databricks/workspaces/privateEndpointConnections"

func newPECClient(t *testing.T) *armdatabricks.PrivateEndpointConnectionsClient {
	t.Helper()

	opts, sub := newARMOptions(t)

	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewPrivateEndpointConnectionsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new PEC client: %v", err)
	}

	return client
}

func createPEC(
	t *testing.T,
	client *armdatabricks.PrivateEndpointConnectionsClient,
	name string,
	status armdatabricks.PrivateLinkServiceConnectionStatus,
	description string,
) armdatabricks.PrivateEndpointConnection {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, testWS, name, armdatabricks.PrivateEndpointConnection{
		Properties: &armdatabricks.PrivateEndpointConnectionProperties{
			PrivateLinkServiceConnectionState: &armdatabricks.PrivateLinkServiceConnectionState{
				Status:      to.Ptr(status),
				Description: to.Ptr(description),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	return res.PrivateEndpointConnection
}

func hasGroupID(groups []*string, want string) bool {
	for _, g := range groups {
		if g != nil && *g == want {
			return true
		}
	}

	return false
}

func TestSDKPrivateEndpointLifecycle(t *testing.T) {
	client := newPECClient(t)
	ctx := context.Background()

	const pecName = "my-pec"

	created := createPEC(t, client, pecName, armdatabricks.PrivateLinkServiceConnectionStatusApproved, "ok")

	if created.Name == nil || *created.Name != pecName {
		t.Fatalf("got name %v, want %q", created.Name, pecName)
	}

	if created.Type == nil || *created.Type != wantPECType {
		t.Fatalf("got type %v, want %q", created.Type, wantPECType)
	}

	if created.Properties == nil {
		t.Fatal("expected properties on created PEC")
	}

	state := created.Properties.PrivateLinkServiceConnectionState
	if state == nil || state.Status == nil ||
		*state.Status != armdatabricks.PrivateLinkServiceConnectionStatusApproved {
		t.Fatalf("expected Approved status, got %+v", state)
	}

	if created.Properties.ProvisioningState == nil ||
		*created.Properties.ProvisioningState != armdatabricks.PrivateEndpointConnectionProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded provisioning state, got %v", created.Properties.ProvisioningState)
	}

	if !hasGroupID(created.Properties.GroupIDs, "databricks_ui_api") {
		t.Fatalf("expected GroupIDs to contain databricks_ui_api, got %v", created.Properties.GroupIDs)
	}

	got, err := client.Get(ctx, testRG, testWS, pecName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != pecName {
		t.Fatalf("Get returned name %v, want %q", got.Name, pecName)
	}

	pager := client.NewListPager(testRG, testWS, nil)

	var count int

	for pager.More() {
		page, errPage := pager.NextPage(ctx)
		if errPage != nil {
			t.Fatalf("List NextPage: %v", errPage)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Fatalf("got %d PECs in list, want 1", count)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, testWS, pecName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone: %v", err)
	}

	if _, err = client.Get(ctx, testRG, testWS, pecName, nil); err == nil {
		t.Fatal("expected error getting PEC after delete")
	}
}

func TestSDKPrivateEndpointMissingWorkspace(t *testing.T) {
	// Do not seed a workspace: build the client directly against a fresh server.
	opts, sub := newARMOptions(t)

	client, err := armdatabricks.NewPrivateEndpointConnectionsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new PEC client: %v", err)
	}

	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, "no-such-workspace", "pec", armdatabricks.PrivateEndpointConnection{
		Properties: &armdatabricks.PrivateEndpointConnectionProperties{
			PrivateLinkServiceConnectionState: &armdatabricks.PrivateLinkServiceConnectionState{
				Status: to.Ptr(armdatabricks.PrivateLinkServiceConnectionStatusApproved),
			},
		},
	}, nil)
	if err == nil {
		if _, err = poller.PollUntilDone(ctx, nil); err == nil {
			t.Fatal("expected error creating PEC on a missing workspace")
		}
	}

	pager := client.NewListPager(testRG, "no-such-workspace", nil)
	if _, err = pager.NextPage(ctx); err == nil {
		t.Fatal("expected error listing PECs on a missing workspace")
	}
}

func TestSDKPrivateEndpointGetMissing(t *testing.T) {
	client := newPECClient(t)

	_, err := client.Get(context.Background(), testRG, testWS, "does-not-exist", nil)
	if err == nil {
		t.Fatal("expected error getting a missing PEC")
	}
}

func TestSDKPrivateEndpointRejectedStatus(t *testing.T) {
	client := newPECClient(t)

	created := createPEC(t, client, "rejected-pec", armdatabricks.PrivateLinkServiceConnectionStatusRejected, "nope")

	if created.Properties == nil || created.Properties.PrivateLinkServiceConnectionState == nil {
		t.Fatalf("expected connection state, got %+v", created.Properties)
	}

	state := created.Properties.PrivateLinkServiceConnectionState
	if state.Status == nil || *state.Status != armdatabricks.PrivateLinkServiceConnectionStatusRejected {
		t.Fatalf("expected Rejected status echoed back, got %+v", state)
	}
}
