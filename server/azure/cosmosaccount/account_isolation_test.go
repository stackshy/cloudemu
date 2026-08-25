// Real-user end-to-end tests for the ARM control plane wired to the Cosmos SQL
// data plane: the canonical flow is armcosmos creates an account and returns
// properties.documentEndpoint, which the azcosmos data-plane client then
// connects to. These tests drive both live SDKs against one emulator and prove:
//   - the ARM-returned documentEndpoint resolves back to the emulator (N3), so a
//     client built purely from it reaches its own account's data;
//   - two accounts are isolated (N2): a database/container in account A is not
//     visible from account B, and each holds independent data;
//   - a container's indexingPolicy round-trips on read (N1).
package cosmosaccount_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// dataPlaneKey is a dummy base64 master key; the data-plane handler ignores auth.
const dataPlaneKey = "dGVzdC1rZXk=" // base64("test-key")

// cosmosStack is one emulator serving both the ARM control plane
// (databaseAccounts) and the Cosmos SQL data plane, with an armcosmos client
// bound to it. Data-plane clients are built per account from the endpoint ARM
// returns, over the same TLS test transport.
type cosmosStack struct {
	arm *armcosmos.DatabaseAccountsClient
	ts  *httptest.Server
}

func newCosmosStack(t *testing.T) *cosmosStack {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	armc, err := armcosmos.NewDatabaseAccountsClient("sub-1", fakeCred{}, opts)
	require.NoError(t, err)

	return &cosmosStack{arm: armc, ts: ts}
}

// createAccountEndpoint provisions an account via ARM and returns the
// documentEndpoint the control plane hands back — exactly what a real user feeds
// to azcosmos.NewClientWithKey.
func (s *cosmosStack) createAccountEndpoint(t *testing.T, rg, name string) string {
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
	require.NoError(t, err)

	created, err := poller.PollUntilDone(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, created.Properties)
	require.NotNil(t, created.Properties.DocumentEndpoint)

	return *created.Properties.DocumentEndpoint
}

// dataClient builds an azcosmos client from an ARM-returned endpoint, over the
// emulator's TLS transport.
func (s *cosmosStack) dataClient(t *testing.T, endpoint string) *azcosmos.Client {
	t.Helper()

	cred, err := azcosmos.NewKeyCredential(dataPlaneKey)
	require.NoError(t, err)

	client, err := azcosmos.NewClientWithKey(endpoint, cred, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: s.ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	require.NoError(t, err)

	return client
}

// wantStatus asserts err is an azcore.ResponseError with the given HTTP status.
func wantStatus(t *testing.T, err error, status int, op string) {
	t.Helper()

	require.Error(t, err, op)

	var respErr *azcore.ResponseError
	require.True(t, errors.As(err, &respErr), "%s: want *azcore.ResponseError, got %T: %v", op, err, err)
	assert.Equal(t, status, respErr.StatusCode, "%s", op)
}

// makeUsersContainer creates database "appdb" / container "users" (pk /pk)
// through client and returns the container client.
func makeUsersContainer(ctx context.Context, t *testing.T, client *azcosmos.Client) *azcosmos.ContainerClient {
	t.Helper()

	_, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "appdb"}, nil)
	require.NoError(t, err)

	db, err := client.NewDatabase("appdb")
	require.NoError(t, err)

	_, err = db.CreateContainer(ctx, azcosmos.ContainerProperties{
		ID:                     "users",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}, nil)
	require.NoError(t, err)

	cc, err := db.NewContainer("users")
	require.NoError(t, err)

	return cc
}

// TestSDKCosmosAccountEndpointResolvesAndIsolates covers N3 (the ARM
// documentEndpoint resolves back to the emulator) and N2 (two accounts are
// isolated): the whole flow runs across two accounts built solely from their
// ARM-returned endpoints.
func TestSDKCosmosAccountEndpointResolvesAndIsolates(t *testing.T) {
	ctx := context.Background()
	stack := newCosmosStack(t)

	endpointA := stack.createAccountEndpoint(t, "rg-a", "acct-a")
	endpointB := stack.createAccountEndpoint(t, "rg-b", "acct-b")
	require.NotEqual(t, endpointA, endpointB, "each account must get a distinct endpoint")

	clientA := stack.dataClient(t, endpointA)
	clientB := stack.dataClient(t, endpointB)

	// N3: a client built purely from account A's ARM endpoint reaches the
	// emulator and can create and read back its own data.
	usersA := makeUsersContainer(ctx, t, clientA)

	docA, err := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "who": "account-a"})
	require.NoError(t, err)
	_, err = usersA.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), docA, nil)
	require.NoError(t, err)

	readA, err := usersA.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	require.NoError(t, err, "endpoint from ARM must resolve back to the emulator")

	var gotA map[string]any
	require.NoError(t, json.Unmarshal(readA.Value, &gotA))
	assert.Equal(t, "account-a", gotA["who"])

	// N2: account B sees NONE of account A's appdb/users — the database and
	// container are absent in account B's namespace.
	dbB, err := clientB.NewDatabase("appdb")
	require.NoError(t, err)

	usersB, err := dbB.NewContainer("users")
	require.NoError(t, err)

	_, err = usersB.Read(ctx, nil)
	wantStatus(t, err, 404, "account B reading account A's container")

	_, err = usersB.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	wantStatus(t, err, 404, "account B reading account A's item")

	// Account B can create its OWN appdb/users with independent contents.
	_, err = clientB.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "appdb"}, nil)
	require.NoError(t, err)
	_, err = dbB.CreateContainer(ctx, azcosmos.ContainerProperties{
		ID:                     "users",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}, nil)
	require.NoError(t, err)

	docB, err := json.Marshal(map[string]any{"id": "u1", "pk": "team-a", "who": "account-b"})
	require.NoError(t, err)
	_, err = usersB.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), docB, nil)
	require.NoError(t, err)

	readB, err := usersB.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	require.NoError(t, err)

	var gotB map[string]any
	require.NoError(t, json.Unmarshal(readB.Value, &gotB))
	assert.Equal(t, "account-b", gotB["who"], "account B's item is independent of account A's")

	// Account A's item is untouched by account B's identically keyed write.
	readA2, err := usersA.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	require.NoError(t, err)

	var gotA2 map[string]any
	require.NoError(t, json.Unmarshal(readA2.Value, &gotA2))
	assert.Equal(t, "account-a", gotA2["who"], "account A's data must survive account B's write")
}

// TestSDKCosmosIndexingPolicyRoundTrip covers N1: a container's indexingPolicy
// (mode + included/excluded paths) is returned intact on a container read.
func TestSDKCosmosIndexingPolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	stack := newCosmosStack(t)

	endpoint := stack.createAccountEndpoint(t, "rg-1", "idx-acct")
	client := stack.dataClient(t, endpoint)

	_, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "db"}, nil)
	require.NoError(t, err)

	db, err := client.NewDatabase("db")
	require.NoError(t, err)

	_, err = db.CreateContainer(ctx, azcosmos.ContainerProperties{
		ID:                     "c1",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
		IndexingPolicy: &azcosmos.IndexingPolicy{
			Automatic:     true,
			IndexingMode:  azcosmos.IndexingModeConsistent,
			IncludedPaths: []azcosmos.IncludedPath{{Path: "/*"}},
			ExcludedPaths: []azcosmos.ExcludedPath{{Path: "/secret/?"}},
		},
	}, nil)
	require.NoError(t, err)

	cc, err := db.NewContainer("c1")
	require.NoError(t, err)

	resp, err := cc.Read(ctx, nil)
	require.NoError(t, err)

	ip := resp.ContainerProperties.IndexingPolicy
	require.NotNil(t, ip, "indexingPolicy must round-trip on a container read")
	assert.Equal(t, azcosmos.IndexingModeConsistent, ip.IndexingMode)
	require.Len(t, ip.IncludedPaths, 1)
	assert.Equal(t, "/*", ip.IncludedPaths[0].Path)
	require.Len(t, ip.ExcludedPaths, 1)
	assert.Equal(t, "/secret/?", ip.ExcludedPaths[0].Path)
}
