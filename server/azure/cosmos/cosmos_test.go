package cosmos_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// fakeKey is the dummy master key our test client uses. The handler ignores
// the Authorization header completely.
const fakeKey = "dGVzdC1rZXk=" // base64("test-key")

// TestSDKCosmosRoundTrip drives the Azure azcosmos SDK against our handler
// for the data-plane resources: database, container, items.
func TestSDKCosmosRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{CosmosDB: cloudP.CosmosDB})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	cred, err := azcosmos.NewKeyCredential(fakeKey)
	if err != nil {
		t.Fatalf("NewKeyCredential: %v", err)
	}

	opts := &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := azcosmos.NewClientWithKey(ts.URL, cred, opts)
	if err != nil {
		t.Fatalf("NewClientWithKey: %v", err)
	}

	ctx := context.Background()

	// Create database (virtual — handler always succeeds).
	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "appdb"}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase("appdb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	// Create container.
	containerProps := azcosmos.ContainerProperties{
		ID:                     "users",
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
	}

	if _, err := dbClient.CreateContainer(ctx, containerProps, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	contClient, err := dbClient.NewContainer("users")
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	// Create item.
	doc := map[string]any{"id": "u1", "pk": "team-a", "name": "Alice"}
	docBytes, _ := json.Marshal(doc)
	pk := azcosmos.NewPartitionKeyString("team-a")

	createResp, err := contClient.CreateItem(ctx, pk, docBytes, nil)
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if createResp.RawResponse.StatusCode != 201 {
		t.Errorf("CreateItem status=%d want 201", createResp.RawResponse.StatusCode)
	}

	// Read item.
	readResp, err := contClient.ReadItem(ctx, pk, "u1", nil)
	if err != nil {
		t.Fatalf("ReadItem: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(readResp.Value, &got); err != nil {
		t.Fatalf("unmarshal read: %v", err)
	}

	if got["name"] != "Alice" {
		t.Errorf("name=%v want Alice", got["name"])
	}

	// Replace item.
	doc["name"] = "Alice Updated"
	docBytes, _ = json.Marshal(doc)

	if _, err := contClient.ReplaceItem(ctx, pk, "u1", docBytes, nil); err != nil {
		t.Fatalf("ReplaceItem: %v", err)
	}

	// Delete item.
	if _, err := contClient.DeleteItem(ctx, pk, "u1", nil); err != nil {
		t.Errorf("DeleteItem: %v", err)
	}

	// Read deleted item — should 404.
	if _, err := contClient.ReadItem(ctx, pk, "u1", nil); err == nil {
		t.Error("expected error reading deleted item")
	} else {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode != 404 {
			t.Errorf("expected 404, got %d", respErr.StatusCode)
		}
	}

	// Delete container.
	if _, err := contClient.Delete(ctx, nil); err != nil {
		t.Errorf("Container.Delete: %v", err)
	}

	_ = strings.TrimSpace
}
