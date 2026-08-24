package cosmosdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// pkTestContainer creates a container with the given partition-key path and
// returns its client. Used to exercise partition keys other than /pk and /id.
func pkTestContainer(t *testing.T, db, name, pkPath string) *azcosmos.ContainerClient {
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

	ctx := context.Background()
	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: db}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase(db)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     name,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{pkPath}},
	}
	if _, err := dbClient.CreateContainer(ctx, props, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	cc, err := dbClient.NewContainer(name)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	return cc
}

// TestSDKCustomPartitionKeyPointOps exercises a container whose partition key
// is a custom attribute (/category, not /pk or /id): point reads and deletes
// must resolve by the real partition-key value plus the document id, and two
// documents sharing a partition key but differing in id must be independent.
func TestSDKCustomPartitionKeyPointOps(t *testing.T) {
	ctx := context.Background()
	cc := pkTestContainer(t, "catalogdb", "products", "/category")

	pk := azcosmos.NewPartitionKeyString("books")

	for _, d := range []map[string]any{
		{"id": "p1", "category": "books", "title": "Go in Practice"},
		{"id": "p2", "category": "books", "title": "The Go Programming Language"},
	} {
		b, _ := json.Marshal(d)
		if _, err := cc.CreateItem(ctx, pk, b, nil); err != nil {
			t.Fatalf("CreateItem(%v): %v", d["id"], err)
		}
	}

	// Point read by (category, id) returns the right document, not a collision.
	resp, err := cc.ReadItem(ctx, pk, "p1", nil)
	if err != nil {
		t.Fatalf("ReadItem p1: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Value, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["id"] != "p1" || got["title"] != "Go in Practice" {
		t.Errorf("ReadItem p1 = id:%v title:%v want p1/Go in Practice", got["id"], got["title"])
	}

	// The sibling under the same partition key is independent.
	resp2, err := cc.ReadItem(ctx, pk, "p2", nil)
	if err != nil {
		t.Fatalf("ReadItem p2: %v", err)
	}

	var got2 map[string]any
	if err := json.Unmarshal(resp2.Value, &got2); err != nil {
		t.Fatalf("unmarshal p2: %v", err)
	}

	if got2["id"] != "p2" {
		t.Errorf("ReadItem p2 id=%v want p2", got2["id"])
	}

	// Delete p1; it is gone and p2 survives.
	if _, err := cc.DeleteItem(ctx, pk, "p1", nil); err != nil {
		t.Fatalf("DeleteItem p1: %v", err)
	}

	if _, err := cc.ReadItem(ctx, pk, "p1", nil); err == nil {
		t.Error("ReadItem p1 after delete: expected 404, got nil error")
	} else {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode != 404 {
			t.Errorf("ReadItem p1 after delete status=%d want 404", respErr.StatusCode)
		}
	}

	if _, err := cc.ReadItem(ctx, pk, "p2", nil); err != nil {
		t.Errorf("ReadItem p2 after deleting p1: %v (p2 must survive)", err)
	}
}

// TestSDKIDPartitionKeyPointOps exercises a container whose partition key is
// /id — the value and the document id coincide — proving that path still reads
// and deletes correctly.
func TestSDKIDPartitionKeyPointOps(t *testing.T) {
	ctx := context.Background()
	cc := pkTestContainer(t, "iddb", "widgets", "/id")

	b, _ := json.Marshal(map[string]any{"id": "w1", "color": "blue"})
	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("w1"), b, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	resp, err := cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("w1"), "w1", nil)
	if err != nil {
		t.Fatalf("ReadItem: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Value, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["color"] != "blue" {
		t.Errorf("color=%v want blue", got["color"])
	}

	if _, err := cc.DeleteItem(ctx, azcosmos.NewPartitionKeyString("w1"), "w1", nil); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	if _, err := cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("w1"), "w1", nil); err == nil {
		t.Error("ReadItem after delete: expected error, got nil")
	}
}
