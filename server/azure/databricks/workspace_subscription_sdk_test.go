package databricks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// wantSubPrefix is the ARM id prefix a resource created under testSub must
// carry. defaultAccountPrefix is the emulator's default-account id that leaked
// into resource ids before the subscription was threaded through.
const (
	wantSubPrefix        = "/subscriptions/sub-1/"
	defaultAccountPrefix = "/subscriptions/123456789012/"
)

// TestSDKWorkspaceIDReflectsSubscription verifies the workspace resource id is
// built from the request's subscription, not the emulator's default account.
// A client scoping role assignments or resource locks from ws.ID would
// otherwise target a non-existent resource.
func TestSDKWorkspaceIDReflectsSubscription(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	ws := createWorkspace(t, client)

	if ws.ID == nil || !strings.HasPrefix(*ws.ID, wantSubPrefix) {
		t.Fatalf("create id = %v, want prefix %q", ws.ID, wantSubPrefix)
	}

	if ws.ID != nil && strings.HasPrefix(*ws.ID, defaultAccountPrefix) {
		t.Fatalf("create id = %v leaked the default account", *ws.ID)
	}

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID == nil || !strings.HasPrefix(*got.ID, wantSubPrefix) {
		t.Fatalf("get id = %v, want prefix %q", got.ID, wantSubPrefix)
	}
}

// TestSDKAccessConnectorIDReflectsSubscription verifies the access-connector id
// is built from the request subscription as well.
func TestSDKAccessConnectorIDReflectsSubscription(t *testing.T) {
	client := newAccessConnectorsClient(t)

	created := createAccessConnector(t, client, "conn-sub", armdatabricks.AccessConnector{
		Location: to.Ptr("eastus"),
	})

	if created.ID == nil || !strings.HasPrefix(*created.ID, wantSubPrefix) {
		t.Fatalf("access connector id = %v, want prefix %q", created.ID, wantSubPrefix)
	}

	if created.ID != nil && strings.HasPrefix(*created.ID, defaultAccountPrefix) {
		t.Fatalf("access connector id = %v leaked the default account", *created.ID)
	}
}

// TestSDKWorkspaceChildIDReflectsSubscription verifies a workspace sub-resource
// (a private-endpoint connection here) inherits its parent's subscription in its
// id, rather than defaulting to the emulator account.
func TestSDKWorkspaceChildIDReflectsSubscription(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	pecClient, err := armdatabricks.NewPrivateEndpointConnectionsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new PEC client: %v", err)
	}

	pec := createPEC(t, pecClient, "pec-1", armdatabricks.PrivateLinkServiceConnectionStatusApproved, "ok")

	if pec.ID == nil || !strings.HasPrefix(*pec.ID, wantSubPrefix) {
		t.Fatalf("PEC id = %v, want prefix %q", pec.ID, wantSubPrefix)
	}

	if pec.ID != nil && strings.HasPrefix(*pec.ID, defaultAccountPrefix) {
		t.Fatalf("PEC id = %v leaked the default account", *pec.ID)
	}
}
