package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// These tests prove that ARM DELETE is idempotent through the real armdatabricks
// clients: deleting an already-gone (or never-created) #209 resource must not
// error. The emulator answers such a delete with 204 No Content, so an SDK
// BeginDelete -> PollUntilDone reports success rather than surfacing a 404.

// idempotentDeleteAC runs BeginDelete + PollUntilDone on an access connector and
// fails the test if either step returns an error.
func idempotentDeleteAC(t *testing.T, client *armdatabricks.AccessConnectorsClient, rg, name string) {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginDelete(%q): %v", name, err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone(%q): %v", name, err)
	}
}

func TestSDKAccessConnectorDeleteIdempotent(t *testing.T) {
	opts, sub := newARMOptions(t)

	client, err := armdatabricks.NewAccessConnectorsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new access connectors client: %v", err)
	}

	ctx := context.Background()

	const connName = "conn-idempotent"

	// Create a connector so the first delete has a real target.
	createPoller, err := client.BeginCreateOrUpdate(ctx, testRG, connName, armdatabricks.AccessConnector{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	// First delete removes it, second delete on the now-missing connector must
	// still succeed (idempotent 204).
	idempotentDeleteAC(t, client, testRG, connName)
	idempotentDeleteAC(t, client, testRG, connName)

	// Deleting a connector that was never created must also succeed.
	idempotentDeleteAC(t, client, testRG, "never-existed")
}

func TestSDKPrivateEndpointDeleteIdempotent(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewPrivateEndpointConnectionsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new PEC client: %v", err)
	}

	ctx := context.Background()

	// Deleting a PEC that was never created on a live workspace must not error.
	poller, err := client.BeginDelete(ctx, testRG, testWS, "never-existed", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone on missing PEC: %v", err)
	}
}

func TestSDKVNetPeeringDeleteIdempotent(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewVNetPeeringClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new vnet peering client: %v", err)
	}

	ctx := context.Background()

	// Deleting a peering that was never created on a live workspace must not error.
	poller, err := client.BeginDelete(ctx, testRG, testWS, "never-existed", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone on missing peering: %v", err)
	}
}
