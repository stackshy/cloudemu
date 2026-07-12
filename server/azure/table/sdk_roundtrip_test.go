package table_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKTableRoundTrip drives Azure Table Storage operations with the real
// aztables client against our handler. aztables supports anonymous access, so
// the test doesn't have to forge SharedKey signatures.
func TestSDKTableRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{TableStorage: cloudP.TableStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svcOpts := &aztables.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	svc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", svcOpts)
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	const tableName = "people"

	// Create table.
	if _, err := svc.CreateTable(ctx, tableName, nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	client := svc.NewClient(tableName)

	// Insert entity.
	entity := map[string]any{
		"PartitionKey": "org",
		"RowKey":       "alice",
		"Email":        "alice@example.com",
		"Age":          int64(30),
	}

	marshalled, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}

	if _, err := client.AddEntity(ctx, marshalled, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Get entity and verify properties round-trip.
	got, err := client.GetEntity(ctx, "org", "alice", nil)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	var props map[string]any
	if err := json.Unmarshal(got.Value, &props); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}

	if props["Email"] != "alice@example.com" {
		t.Errorf("Email mismatch: got=%v want=alice@example.com", props["Email"])
	}

	if props["PartitionKey"] != "org" || props["RowKey"] != "alice" {
		t.Errorf("key mismatch: pk=%v rk=%v", props["PartitionKey"], props["RowKey"])
	}

	// Query by partition and confirm alice is returned.
	{
		filter := "PartitionKey eq 'org'"
		pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Filter: &filter})

		seen := map[string]bool{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("ListEntities: %v", err)
			}

			for _, raw := range page.Entities {
				var e map[string]any
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatalf("unmarshal query entity: %v", err)
				}

				if rk, ok := e["RowKey"].(string); ok {
					seen[rk] = true
				}
			}
		}

		if !seen["alice"] {
			t.Errorf("alice not returned by partition query: %v", seen)
		}
	}

	// Delete entity.
	if _, err := client.DeleteEntity(ctx, "org", "alice", nil); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// Confirm the entity is gone.
	if _, err := client.GetEntity(ctx, "org", "alice", nil); err == nil ||
		!strings.Contains(err.Error(), "ResourceNotFound") {
		t.Errorf("expected ResourceNotFound after delete, got %v", err)
	}

	// Delete table.
	if _, err := client.Delete(ctx, nil); err != nil {
		t.Fatalf("Delete table: %v", err)
	}
}
