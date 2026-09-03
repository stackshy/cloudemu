package azure_test

// Real-user end-to-end proof that an Azure Functions cosmosDBTrigger binding
// actually fires: a function app declares a cosmosDBTrigger binding naming a
// (databaseName, containerName) via ARM, then a document created or replaced
// in that container with the real azcosmos SDK synchronously invokes the
// function with the changed document (wrapped in a single-element JSON
// array, mirroring Cosmos's change-feed batch shape) as its payload. This is
// the Cosmos DB counterpart of TestQueueStorageTriggerInvokesFunction (#997),
// TestServiceBusTopicTriggerInvokesFunction (#1001) and
// TestBlobStorageTriggerInvokesFunction (#1003): before this wiring a
// cosmosDBTrigger binding round-tripped as CRUD only and no document write
// ever reached the function. Real Cosmos change feed fires on document
// create and update only, never on delete.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newFullAzureTLSServerWithProvider boots the full production Azure server
// over TLS (the Cosmos SDK's usual transport) over a caller-held provider, so
// the test can register a Go handler to observe that a function was actually
// invoked and, for the recursion test, call back into the held provider
// in-process.
func newFullAzureTLSServerWithProvider(t *testing.T) (*httptest.Server, *azureprovider.Provider) {
	t.Helper()

	p := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(p))
	t.Cleanup(ts.Close)

	return ts, p
}

// cosmosFuncAppBase returns the ARM base path of a function app used by
// these tests.
func cosmosFuncAppBase(app string) string {
	return "/subscriptions/sub-ct/resourceGroups/rg-ct/providers/Microsoft.Web/sites/" + app
}

// createCosmosTriggeredApp creates a function app plus one deployed function
// declaring a cosmosDBTrigger binding on (database, container).
func createCosmosTriggeredApp(t *testing.T, ts *httptest.Server, app, database, container string) {
	t.Helper()

	base := cosmosFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"docs","type":"cosmosDBTrigger","direction":"in",`+
			`"databaseName":"`+database+`","containerName":"`+container+`",`+
			`"leaseContainerName":"leases","connectionStringSetting":"CosmosDBConnection"}]}}}`)
}

// newCosmosDataClient returns a real azcosmos client pointed at ts, matching
// server/azure/cosmos_blob_dispatch_test.go's TLS setup (dispatchKey is
// defined there, in this same package).
func newCosmosDataClient(t *testing.T, ts *httptest.Server) *azcosmos.Client {
	t.Helper()

	cred, err := azcosmos.NewKeyCredential(dispatchKey)
	if err != nil {
		t.Fatalf("NewKeyCredential: %v", err)
	}

	client, err := azcosmos.NewClientWithKey(ts.URL, cred, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("NewClientWithKey: %v", err)
	}

	return client
}

// makeCosmosContainer creates database/container (partition key path "/pk")
// through client and returns the container client.
func makeCosmosContainer(ctx context.Context, t *testing.T, client *azcosmos.Client, database, container string) *azcosmos.ContainerClient {
	t.Helper()

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: database}, nil); err != nil {
		t.Fatalf("CreateDatabase(%s): %v", database, err)
	}

	db, err := client.NewDatabase(database)
	if err != nil {
		t.Fatalf("NewDatabase(%s): %v", database, err)
	}

	if _, err := db.CreateContainer(ctx, azcosmos.ContainerProperties{
		ID:                     container,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}, nil); err != nil {
		t.Fatalf("CreateContainer(%s): %v", container, err)
	}

	cc, err := db.NewContainer(container)
	if err != nil {
		t.Fatalf("NewContainer(%s): %v", container, err)
	}

	return cc
}

func TestCosmosDBTriggerInvokesFunction(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "ct-app"
		database  = "orders-db"
		container = "orders"
	)

	createCosmosTriggeredApp(t, ts, app, database, container)

	got := make(chan string, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		select {
		case got <- string(payload):
		default:
		}

		return payload, nil
	})

	cc := makeCosmosContainer(ctx, t, newCosmosDataClient(t, ts), database, container)

	doc, _ := json.Marshal(map[string]any{"id": "o1", "pk": "team-a", "status": "new"})
	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Delivery is synchronous, so the function has fired by the time the
	// create returns.
	select {
	case body := <-got:
		var docs []map[string]any
		if err := json.Unmarshal([]byte(body), &docs); err != nil {
			t.Fatalf("unmarshal trigger payload %q: %v", body, err)
		}

		if len(docs) != 1 {
			t.Fatalf("trigger payload has %d documents, want 1 (a single-element array per write)", len(docs))
		}

		if docs[0]["id"] != "o1" || docs[0]["status"] != "new" {
			t.Fatalf("triggered document = %v, want id=o1 status=new", docs[0])
		}
	default:
		t.Fatal("cosmosDBTrigger function was not invoked")
	}
}

// TestCosmosDBTriggerDifferentContainerDoesNotFire proves a document written
// to a container the function is not bound to invokes nothing, even within
// the same database.
func TestCosmosDBTriggerDifferentContainerDoesNotFire(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app      = "ct-app2"
		database = "orders-db2"
	)

	createCosmosTriggeredApp(t, ts, app, database, "orders")

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}
		return payload, nil
	})

	client := newCosmosDataClient(t, ts)
	cc := makeCosmosContainer(ctx, t, client, database, "shipments")

	doc, _ := json.Marshal(map[string]any{"id": "s1", "pk": "team-a"})
	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("function fired for a container it is not bound to")
	default:
	}
}

// TestCosmosDBTriggerDifferentDatabaseDoesNotFire proves a document written
// to a same-named container in a different database invokes nothing: both
// databaseName and containerName must match.
func TestCosmosDBTriggerDifferentDatabaseDoesNotFire(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const app = "ct-app3"

	createCosmosTriggeredApp(t, ts, app, "orders-db3", "orders")

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}
		return payload, nil
	})

	client := newCosmosDataClient(t, ts)
	cc := makeCosmosContainer(ctx, t, client, "other-db3", "orders")

	doc, _ := json.Marshal(map[string]any{"id": "o1", "pk": "team-a"})
	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("function fired for a database it is not bound to")
	default:
	}
}

// TestCosmosDBTriggerDeleteDoesNotFire proves that, matching real Cosmos
// change feed, a document DELETE never fires a cosmosDBTrigger: the bound
// function fires once on create, then not again when that same document is
// deleted.
func TestCosmosDBTriggerDeleteDoesNotFire(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "ct-app4"
		database  = "orders-db4"
		container = "orders"
	)

	createCosmosTriggeredApp(t, ts, app, database, container)

	var invocations int32

	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)
		return payload, nil
	})

	client := newCosmosDataClient(t, ts)
	cc := makeCosmosContainer(ctx, t, client, database, container)

	pk := azcosmos.NewPartitionKeyString("team-a")

	doc, _ := json.Marshal(map[string]any{"id": "o1", "pk": "team-a"})
	if _, err := cc.CreateItem(ctx, pk, doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 1 {
		t.Fatalf("invocations after create = %d, want 1", got)
	}

	if _, err := cc.DeleteItem(ctx, pk, "o1", nil); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 1 {
		t.Fatalf("invocations after delete = %d, want it to stay at 1 (delete must not fire a cosmosDBTrigger)", got)
	}
}

// TestCosmosDBTriggerReplaceFires proves a document UPDATE (replace) fires
// the trigger again, matching real Cosmos change feed's create-AND-update
// semantics.
func TestCosmosDBTriggerReplaceFires(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "ct-app5"
		database  = "orders-db5"
		container = "orders"
	)

	createCosmosTriggeredApp(t, ts, app, database, container)

	var invocations int32

	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)
		return payload, nil
	})

	client := newCosmosDataClient(t, ts)
	cc := makeCosmosContainer(ctx, t, client, database, container)

	pk := azcosmos.NewPartitionKeyString("team-a")

	doc, _ := json.Marshal(map[string]any{"id": "o1", "pk": "team-a", "status": "new"})
	if _, err := cc.CreateItem(ctx, pk, doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	doc, _ = json.Marshal(map[string]any{"id": "o1", "pk": "team-a", "status": "shipped"})
	if _, err := cc.ReplaceItem(ctx, pk, "o1", doc, nil); err != nil {
		t.Fatalf("ReplaceItem: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != 2 {
		t.Fatalf("invocations after create+replace = %d, want 2", got)
	}
}

// TestCosmosDBTriggerDisabledFunctionSkipped proves a disabled deployed
// function does not fire even though its binding matches the written
// (database, container).
func TestCosmosDBTriggerDisabledFunctionSkipped(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "ct-disabled-app"
		database  = "orders-db6"
		container = "orders"
	)

	base := cosmosFuncAppBase(app)
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"isDisabled":true,"config":{"bindings":[`+
			`{"name":"docs","type":"cosmosDBTrigger","direction":"in",`+
			`"databaseName":"`+database+`","containerName":"`+container+`",`+
			`"connectionStringSetting":"CosmosDBConnection"}]}}}`)

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}
		return payload, nil
	})

	client := newCosmosDataClient(t, ts)
	cc := makeCosmosContainer(ctx, t, client, database, container)

	doc, _ := json.Marshal(map[string]any{"id": "o1", "pk": "team-a"})
	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), doc, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("a disabled function must not fire")
	default:
	}
}

// TestCosmosDBTriggerRecursionGuard proves a function that writes back into
// its own monitored container terminates at recursionguard.MaxDepth rather
// than recursing unbounded, mirroring TestBlobStorageTriggerRecursionGuard.
// The handler forwards the ctx it was invoked with into a direct
// p.CosmosDB.PutItem call (not a fresh HTTP round trip through the real SDK —
// an HTTP hop cannot carry ctx's depth value, only the DepthHeader-based
// webhook path can) so that ctx-carried depth is the channel the guard rides
// on, exactly like the blobTrigger recursion test's direct PutObject call.
// The table name mirrors server/azure/cosmosdb's qualify() encoding for the
// default (unaccounted) account: "{database}/{container}".
func TestCosmosDBTriggerRecursionGuard(t *testing.T) {
	ts, p := newFullAzureTLSServerWithProvider(t)
	ctx := context.Background()

	const (
		app       = "ct-loop-app"
		database  = "loop-db"
		container = "loop-container"
	)

	createCosmosTriggeredApp(t, ts, app, database, container)

	client := newCosmosDataClient(t, ts)
	_ = makeCosmosContainer(ctx, t, client, database, container)

	table := database + "/" + container

	var invocations int32

	p.Functions.RegisterHandler(app, func(ctx context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&invocations, 1)

		err := p.CosmosDB.PutItem(ctx, table, map[string]any{"id": "again", "pk": "team-a"})

		return payload, err
	})

	// The single top-level write that starts the chain.
	seed, _ := json.Marshal(map[string]any{"id": "seed", "pk": "team-a"})

	cc, err := client.NewDatabase(database)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	seedContainer, err := cc.NewContainer(container)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	if _, err := seedContainer.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), seed, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if got := atomic.LoadInt32(&invocations); got != int32(recursionguard.MaxDepth) {
		t.Fatalf("handler invoked %d times, want exactly %d (recursive-loop guard did not bound the chain)",
			got, recursionguard.MaxDepth)
	}
}
