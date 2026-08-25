// This file exercises Cosmos's UniqueKeyPolicy: the HIGH bug where a
// container's declared unique keys were accepted at create time but never
// persisted or enforced (GET echoed UniqueKeyPolicy as nil, and a duplicate
// value on the declared unique key succeeded instead of 409ing).

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// uniqueKeyContainer creates a container with a UniqueKeyPolicy on /email and
// returns its client.
func uniqueKeyContainer(ctx context.Context, t *testing.T, e *cosmosEnv, db, name string) *azcosmos.ContainerClient {
	t.Helper()

	if _, err := e.client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: db}, nil); err != nil {
		t.Fatalf("CreateDatabase(%s): %v", db, err)
	}

	dbClient, err := e.client.NewDatabase(db)
	if err != nil {
		t.Fatalf("NewDatabase(%s): %v", db, err)
	}

	props := azcosmos.ContainerProperties{
		ID:                     name,
		PartitionKeyDefinition: azcosmos.PartitionKeyDefinition{Paths: []string{"/pk"}},
		UniqueKeyPolicy: &azcosmos.UniqueKeyPolicy{
			UniqueKeys: []azcosmos.UniqueKey{{Paths: []string{"/email"}}},
		},
	}
	if _, err := dbClient.CreateContainer(ctx, props, nil); err != nil {
		t.Fatalf("CreateContainer(%s): %v", name, err)
	}

	cc, err := dbClient.NewContainer(name)
	if err != nil {
		t.Fatalf("NewContainer(%s): %v", name, err)
	}

	return cc
}

// TestSDKUniqueKeyPolicyEchoed asserts a container's declared UniqueKeyPolicy
// round-trips through Read.
func TestSDKUniqueKeyPolicyEchoed(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)
	cc := uniqueKeyContainer(ctx, t, e, "ukwiredb", "users")

	resp, err := cc.Read(ctx, nil)
	if err != nil {
		t.Fatalf("Read container: %v", err)
	}

	if resp.ContainerProperties == nil || resp.ContainerProperties.UniqueKeyPolicy == nil {
		t.Fatalf("Read container UniqueKeyPolicy = nil, want /email unique key")
	}

	uk := resp.ContainerProperties.UniqueKeyPolicy.UniqueKeys
	if len(uk) != 1 || len(uk[0].Paths) != 1 || uk[0].Paths[0] != "/email" {
		t.Errorf("Read container UniqueKeyPolicy = %+v, want [{Paths:[/email]}]", uk)
	}
}

// TestSDKUniqueKeyDuplicateRejected asserts creating a second document in the
// same partition with the same /email value 409s, and a distinct value or a
// different partition both succeed.
func TestSDKUniqueKeyDuplicateRejected(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)
	cc := uniqueKeyContainer(ctx, t, e, "ukenforcedb", "users")

	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "u1", "pk": "team-a", "email": "alice@example.com"})

	// Same partition, same email, different id: real Cosmos 409s.
	dup := map[string]any{"id": "u2", "pk": "team-a", "email": "alice@example.com"}
	b, err := json.Marshal(dup)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), b, nil)
	wantRespErr(t, err, 409, "CreateItem duplicate unique key within partition")

	// Same partition, different email: allowed.
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "u3", "pk": "team-a", "email": "carol@example.com"})

	// Different partition, same email: real Cosmos scopes the constraint to a
	// single partition-key value, so this is also allowed.
	createDoc(ctx, t, cc, "team-b", map[string]any{"id": "u4", "pk": "team-b", "email": "alice@example.com"})
}

// TestSDKUniqueKeyMissingFieldAllowed asserts a document that never sets the
// declared unique-key path never collides, matching real Cosmos.
func TestSDKUniqueKeyMissingFieldAllowed(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)
	cc := uniqueKeyContainer(ctx, t, e, "ukmissingdb", "users")

	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "u1", "pk": "team-a"})
	createDoc(ctx, t, cc, "team-a", map[string]any{"id": "u2", "pk": "team-a"})
}
