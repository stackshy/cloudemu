package cosmosdb_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newThroughputClient mounts a fresh Azure Cosmos data plane and returns a real
// azcosmos client pointed at it.
func newThroughputClient(t *testing.T) *azcosmos.Client {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	cred, err := azcosmos.NewKeyCredential(fakeKey)
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

// TestSDKContainerThroughput drives the azcosmos ReadThroughput / ReplaceThroughput
// flow end-to-end against the /offers resource: a container created with manual
// throughput reports it back, and a replace round-trips the new value.
func TestSDKContainerThroughput(t *testing.T) {
	ctx := context.Background()
	client := newThroughputClient(t)

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "tpdb"}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase("tpdb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     "metered",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}
	manual := azcosmos.NewManualThroughputProperties(400)

	if _, err := dbClient.CreateContainer(ctx, props, &azcosmos.CreateContainerOptions{
		ThroughputProperties: &manual,
	}); err != nil {
		t.Fatalf("CreateContainer with throughput: %v", err)
	}

	cc, err := dbClient.NewContainer("metered")
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	// ReadThroughput reflects the provisioned RU/s.
	readResp, err := cc.ReadThroughput(ctx, nil)
	if err != nil {
		t.Fatalf("ReadThroughput: %v", err)
	}

	got, ok := readResp.ThroughputProperties.ManualThroughput()
	if !ok || got != 400 {
		t.Errorf("ReadThroughput manual=%d ok=%v want 400", got, ok)
	}

	// ReplaceThroughput to 1000 and read it back.
	updated := azcosmos.NewManualThroughputProperties(1000)
	if _, err := cc.ReplaceThroughput(ctx, updated, nil); err != nil {
		t.Fatalf("ReplaceThroughput: %v", err)
	}

	after, err := cc.ReadThroughput(ctx, nil)
	if err != nil {
		t.Fatalf("ReadThroughput after replace: %v", err)
	}

	got, ok = after.ThroughputProperties.ManualThroughput()
	if !ok || got != 1000 {
		t.Errorf("post-replace manual=%d ok=%v want 1000", got, ok)
	}
}

// TestSDKContainerThroughputAbsent asserts that a container created without
// provisioned throughput has no dedicated offer, so ReadThroughput 404s — the
// same behavior the real service exposes for shared/serverless containers.
func TestSDKContainerThroughputAbsent(t *testing.T) {
	ctx := context.Background()
	client := newThroughputClient(t)

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "shareddb"}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase("shareddb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     "unmetered",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}
	if _, err := dbClient.CreateContainer(ctx, props, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	cc, err := dbClient.NewContainer("unmetered")
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	if _, err := cc.ReadThroughput(ctx, nil); err == nil {
		t.Fatal("ReadThroughput on a container with no offer: expected error, got nil")
	}
}
