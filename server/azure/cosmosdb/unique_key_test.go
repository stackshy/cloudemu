// This file exercises Cosmos's UniqueKeyPolicy: the HIGH bug where a
// container's declared unique keys were accepted at create time but never
// persisted or enforced (GET echoed UniqueKeyPolicy as nil, and a duplicate
// value on the declared unique key succeeded instead of 409ing).

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

// TestSDKUniqueKeyConcurrentCreateSingleWinner is the regression test for the
// check-then-act race in unique-key enforcement: checkUniqueKeys scans the
// container and decides, then PutItem writes, with nothing held across the two.
// Two concurrent creates carrying the same (partition, unique-key value) could
// both pass the scan and both insert, so more than one 201 was returned instead
// of a single winner and 409s for the rest. It fires N concurrent creates that
// all share one (partition="team-a", email="race@example.com") and asserts
// EXACTLY one succeeds. Filler documents widen the scan window the race lives
// in. This is a logical (check-then-act) race, so -race does not surface it —
// the assertion on the winner count is what fails pre-fix.
func TestSDKUniqueKeyConcurrentCreateSingleWinner(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)

	const (
		dbName   = "ukracedb"
		collName = "users"
		fillers  = 3000
		racers   = 50
	)

	cc := uniqueKeyContainer(ctx, t, e, dbName, collName)

	// Preload filler docs straight through the shared driver (fast, in-memory).
	// They share partition "team-a" with distinct emails, so none collide with
	// each other or the racers — they only make checkUniqueKeys' full-table Scan
	// walk more items, widening the check-then-write window.
	table := dbName + "/" + collName

	for i := range fillers {
		filler := map[string]any{
			"id":    fmt.Sprintf("filler-%d", i),
			"pk":    "team-a",
			"email": fmt.Sprintf("filler-%d@example.com", i),
		}
		if err := e.provider.CosmosDB.PutItem(ctx, table, filler); err != nil {
			t.Fatalf("preload filler %d: %v", i, err)
		}
	}

	var (
		wg        sync.WaitGroup
		successes atomic.Int32
		conflicts atomic.Int32
		others    atomic.Int32
	)

	start := make(chan struct{})

	for i := range racers {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			doc := map[string]any{
				"id":    fmt.Sprintf("racer-%d", idx),
				"pk":    "team-a",
				"email": "race@example.com",
			}

			b, err := json.Marshal(doc)
			if err != nil {
				others.Add(1)
				return
			}

			<-start // release all goroutines together to maximize overlap

			resp, cerr := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("team-a"), b, nil)

			switch {
			case cerr == nil && resp.RawResponse.StatusCode == 201:
				successes.Add(1)
			case isRespStatus(cerr, 409):
				conflicts.Add(1)
			default:
				others.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Errorf("concurrent same-unique-key create: %d succeeded, want exactly 1", got)
	}

	if got := conflicts.Load(); got != racers-1 {
		t.Errorf("concurrent same-unique-key create: %d got 409, want %d", got, racers-1)
	}

	if got := others.Load(); got != 0 {
		t.Errorf("concurrent same-unique-key create: %d unexpected outcomes, want 0", got)
	}
}

// isRespStatus reports whether err is an azcore.ResponseError with the given
// HTTP status. Unlike wantRespErr it returns a bool, so a goroutine can classify
// an outcome without failing the test from a non-test goroutine.
func isRespStatus(err error, status int) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == status
}
