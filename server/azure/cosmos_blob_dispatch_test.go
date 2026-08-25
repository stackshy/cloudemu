// Regression tests for the Cosmos-vs-Blob dispatch boundary on the shared
// standalone listener, where NewFromProvider mounts the Cosmos SQL data plane
// AND the permissive BlobStorage fallback on one server.
//
// The Cosmos data plane peels an optional leading /{account} path segment, but
// it must peel ONLY when that segment names a registered databaseAccount.
// Otherwise a blob path whose first segment happens to be a virtual-directory
// prefix like "dbs/" or "offers/" would be stolen by Cosmos (routed to a
// data-plane collection that answers PUT with 405) instead of served by Blob.
// These tests drive the fully-assembled server and prove Blob keeps those paths
// while real Cosmos accounts stay reachable and isolated.
package azure_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// dispatchKey is a dummy base64 master key; the data-plane handler ignores auth.
const dispatchKey = "dGVzdC1rZXk=" // base64("test-key")

// sharedStack is the fully-assembled Azure server (blob + cosmos data plane +
// cosmos ARM control plane, all coexisting on one TLS listener), with an
// armcosmos client bound to it.
type sharedStack struct {
	arm *armcosmos.DatabaseAccountsClient
	ts  *httptest.Server
}

func newSharedStack(t *testing.T) *sharedStack {
	t.Helper()

	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	armc, err := armcosmos.NewDatabaseAccountsClient("sub-1", fakeCred{}, &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("NewDatabaseAccountsClient: %v", err)
	}

	return &sharedStack{arm: armc, ts: ts}
}

// createAccountEndpoint provisions an account via ARM and returns the
// documentEndpoint the control plane hands back.
func (s *sharedStack) createAccountEndpoint(t *testing.T, rg, name string) string {
	t.Helper()

	poller, err := s.arm.BeginCreateOrUpdate(context.Background(), rg, name, armcosmos.DatabaseAccountCreateUpdateParameters{
		Location: to.Ptr("eastus"),
		Kind:     to.Ptr(armcosmos.DatabaseAccountKindGlobalDocumentDB),
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			DatabaseAccountOfferType: to.Ptr("Standard"),
			Locations: []*armcosmos.Location{
				{LocationName: to.Ptr("eastus"), FailoverPriority: to.Ptr[int32](0)},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate(%s): %v", name, err)
	}

	created, err := poller.PollUntilDone(context.Background(), nil)
	if err != nil {
		t.Fatalf("PollUntilDone(%s): %v", name, err)
	}

	if created.Properties == nil || created.Properties.DocumentEndpoint == nil {
		t.Fatalf("account %s: missing documentEndpoint", name)
	}

	return *created.Properties.DocumentEndpoint
}

// dataClient builds an azcosmos client from an ARM-returned endpoint, over the
// emulator's TLS transport.
func (s *sharedStack) dataClient(t *testing.T, endpoint string) *azcosmos.Client {
	t.Helper()

	cred, err := azcosmos.NewKeyCredential(dispatchKey)
	if err != nil {
		t.Fatalf("NewKeyCredential: %v", err)
	}

	client, err := azcosmos.NewClientWithKey(endpoint, cred, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: s.ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("NewClientWithKey: %v", err)
	}

	return client
}

// TestCosmosDoesNotStealBlobPaths proves that with blob and cosmos coexisting
// and NO account named after the leading segment, a blob path whose first
// segment is a "dbs"/"offers" virtual-directory prefix is served by the Blob
// handler — not stolen by the Cosmos data plane (which would answer PUT with a
// 405). The Blob handler stamps X-Ms-Version on every response; the Cosmos
// handler never does, so that header proves Blob served the request.
func TestCosmosDoesNotStealBlobPaths(t *testing.T) {
	stack := newSharedStack(t)

	for _, path := range []string{"/mycontainer/dbs", "/mycontainer/offers"} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, stack.ts.URL+path, http.NoBody)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := stack.ts.Client().Do(req)
			if err != nil {
				t.Fatalf("PUT %s: %v", path, err)
			}

			defer resp.Body.Close()

			_, _ = io.Copy(io.Discard, resp.Body)

			if resp.Header.Get("X-Ms-Version") == "" {
				t.Errorf("PUT %s: no X-Ms-Version header — the Cosmos data plane stole this blob path "+
					"instead of letting the Blob fallback serve it (status %d)", path, resp.StatusCode)
			}

			if resp.StatusCode == http.StatusMethodNotAllowed {
				t.Errorf("PUT %s: got 405 — routed to the Cosmos data plane, not Blob", path)
			}
		})
	}
}

// TestCosmosAccountCRUDStillIsolated proves the fix did not regress the real
// per-account flow on the shared listener: an ARM-created account's endpoint
// resolves back to the emulator, its CRUD works, and a second account cannot see
// the first account's data.
func TestCosmosAccountCRUDStillIsolated(t *testing.T) {
	ctx := context.Background()
	stack := newSharedStack(t)

	endpointA := stack.createAccountEndpoint(t, "rg-a", "acct-a")
	endpointB := stack.createAccountEndpoint(t, "rg-b", "acct-b")

	if endpointA == endpointB {
		t.Fatalf("accounts must get distinct endpoints, both %q", endpointA)
	}

	clientA := stack.dataClient(t, endpointA)
	clientB := stack.dataClient(t, endpointB)

	usersA := makeAccountContainer(ctx, t, clientA)

	docA, _ := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "who": "account-a"})
	if _, err := usersA.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), docA, nil); err != nil {
		t.Fatalf("account A CreateItem: %v", err)
	}

	readA, err := usersA.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	if err != nil {
		t.Fatalf("account A ReadItem (endpoint must resolve back to the emulator): %v", err)
	}

	var gotA map[string]any
	if err := json.Unmarshal(readA.Value, &gotA); err != nil {
		t.Fatalf("unmarshal A: %v", err)
	}

	if gotA["who"] != "account-a" {
		t.Errorf("account A doc who=%v want account-a", gotA["who"])
	}

	// Account B sees none of account A's appdb/users.
	dbB, err := clientB.NewDatabase("appdb")
	if err != nil {
		t.Fatalf("account B NewDatabase: %v", err)
	}

	usersB, err := dbB.NewContainer("users")
	if err != nil {
		t.Fatalf("account B NewContainer: %v", err)
	}

	if _, err := usersB.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil); err == nil {
		t.Errorf("account B read account A's item: want error, got nil (accounts not isolated)")
	}
}

// TestCosmosAccountNamedDbsReachable is the bonus case: an account named
// literally "dbs" (a valid Cosmos account name) is reachable through its
// ARM-returned endpoint, because splitAccount peels the leading segment when it
// names a registered account even if that name collides with the "dbs"
// data-plane keyword.
func TestCosmosAccountNamedDbsReachable(t *testing.T) {
	ctx := context.Background()
	stack := newSharedStack(t)

	endpoint := stack.createAccountEndpoint(t, "rg-dbs", "dbs")
	client := stack.dataClient(t, endpoint)

	users := makeAccountContainer(ctx, t, client)

	doc, _ := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "who": "in-dbs-account"})
	if _, err := users.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), doc, nil); err != nil {
		t.Fatalf(`account "dbs" CreateItem: %v`, err)
	}

	read, err := users.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	if err != nil {
		t.Fatalf(`account "dbs" ReadItem: %v`, err)
	}

	var got map[string]any
	if err := json.Unmarshal(read.Value, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["who"] != "in-dbs-account" {
		t.Errorf(`account "dbs" doc who=%v want in-dbs-account`, got["who"])
	}
}

// makeAccountContainer creates database "appdb" / container "users" (pk "/pk")
// through client and returns the container client.
func makeAccountContainer(ctx context.Context, t *testing.T, client *azcosmos.Client) *azcosmos.ContainerClient {
	t.Helper()

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "appdb"}, nil); err != nil {
		t.Fatalf("CreateDatabase(appdb): %v", err)
	}

	db, err := client.NewDatabase("appdb")
	if err != nil {
		t.Fatalf("NewDatabase(appdb): %v", err)
	}

	if _, err := db.CreateContainer(ctx, azcosmos.ContainerProperties{
		ID:                     "users",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}, nil); err != nil {
		t.Fatalf("CreateContainer(users): %v", err)
	}

	cc, err := db.NewContainer("users")
	if err != nil {
		t.Fatalf("NewContainer(users): %v", err)
	}

	return cc
}
