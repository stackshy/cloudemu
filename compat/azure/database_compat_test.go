package azure

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// fakeCosmosKey is the dummy master key the client uses; the handler ignores
// the Authorization header entirely. base64("test-key").
const fakeCosmosKey = "dGVzdC1rZXk="

const (
	dbName   = "appdb"
	collName = "users"
	pkPath   = "/pk"
	pkValue  = "team-a"
	itemID   = "u1"
)

// TestCosmosDBdatabaseCompat drives the real azcosmos SDK against CloudEmu's
// in-process Azure wire server and records one result per portable database
// operation the Cosmos handler routes.
func TestCosmosDBdatabaseCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{CosmosDB: provider.CosmosDB})

	cred, err := azcosmos.NewKeyCredential(fakeCosmosKey)
	if err != nil {
		t.Fatalf("NewKeyCredential: %v", err)
	}

	opts := &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := azcosmos.NewClientWithKey(sess.Endpoint(), cred, opts)
	if err != nil {
		t.Fatalf("NewClientWithKey: %v", err)
	}

	ctx := context.Background()

	// The Cosmos database layer is virtual in the handler; create it so the
	// SDK client can resolve database and container scopes.
	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: dbName}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase(dbName)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	// CreateTable — CreateContainer maps to the driver's CreateTable.
	sess.Op("database", "CreateTable", func() error {
		props := azcosmos.ContainerProperties{
			ID:                     collName,
			PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{pkPath}},
		}
		_, cerr := dbClient.CreateContainer(ctx, props, nil)

		return cerr
	})

	contClient, err := dbClient.NewContainer(collName)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	// DescribeTable — reading container properties maps to DescribeTable.
	sess.Op("database", "DescribeTable", func() error {
		_, cerr := contClient.Read(ctx, nil)

		return cerr
	})

	pk := azcosmos.NewPartitionKeyString(pkValue)
	doc := map[string]any{"id": itemID, "pk": pkValue, "name": "Alice"}

	// PutItem — CreateItem maps to the driver's PutItem.
	sess.Op("database", "PutItem", func() error {
		docBytes, merr := json.Marshal(doc)
		if merr != nil {
			return merr
		}
		_, cerr := contClient.CreateItem(ctx, pk, docBytes, nil)

		return cerr
	})

	// GetItem — ReadItem maps to the driver's GetItem; verify the round-trip.
	sess.Op("database", "GetItem", func() error {
		resp, cerr := contClient.ReadItem(ctx, pk, itemID, nil)
		if cerr != nil {
			return cerr
		}

		var got map[string]any
		if uerr := json.Unmarshal(resp.Value, &got); uerr != nil {
			return uerr
		}

		if got["name"] != "Alice" {
			t.Errorf("GetItem name=%v want Alice", got["name"])
		}

		return nil
	})

	// Scan — a query over the container maps to the driver's Scan.
	sess.Op("database", "Scan", func() error {
		pager := contClient.NewQueryItemsPager("SELECT * FROM c", pk, nil)
		seen := 0

		for pager.More() {
			page, perr := pager.NextPage(ctx)
			if perr != nil {
				return perr
			}

			seen += len(page.Items)
		}

		if seen == 0 {
			t.Error("Scan returned no items")
		}

		return nil
	})

	// DeleteItem — maps to the driver's DeleteItem.
	sess.Op("database", "DeleteItem", func() error {
		_, cerr := contClient.DeleteItem(ctx, pk, itemID, nil)

		return cerr
	})

	// DeleteTable — DeleteContainer maps to the driver's DeleteTable.
	sess.Op("database", "DeleteTable", func() error {
		_, cerr := contClient.Delete(ctx, nil)

		return cerr
	})
}
