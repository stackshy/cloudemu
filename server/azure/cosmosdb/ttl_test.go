// This file exercises Cosmos's own container-level default TTL
// (ContainerProperties.DefaultTimeToLive), as opposed to the generic
// driver-level TTLConfig exercised by TestCosmosTTL in cosmos_lifecycle_test.go.
// A real Cosmos client sets defaultTtl on the container itself (no driver call
// involved), so these tests drive container create/read and document
// create/read entirely through the real azcosmos SDK.

package cosmosdb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/stackshy/cloudemu/v2/config"
)

// ttlContainer creates a container with the given DefaultTimeToLive (nil
// leaves TTL disabled) and returns its client.
func ttlContainer(ctx context.Context, t *testing.T, e *cosmosEnv, db, name string, defaultTTL *int32) *azcosmos.ContainerClient {
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
		DefaultTimeToLive:      defaultTTL,
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

// TestSDKContainerDefaultTTLEchoed asserts a container created with
// DefaultTimeToLive echoes it back on Read — the HIGH bug: defaultTtl was
// silently dropped and never persisted.
func TestSDKContainerDefaultTTLEchoed(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)
	cc := ttlContainer(ctx, t, e, "ttlwiredb", "events", to.Ptr[int32](3600))

	resp, err := cc.Read(ctx, nil)
	if err != nil {
		t.Fatalf("Read container: %v", err)
	}

	if resp.ContainerProperties == nil || resp.ContainerProperties.DefaultTimeToLive == nil {
		t.Fatalf("Read container DefaultTimeToLive = nil, want 3600")
	}

	if got := *resp.ContainerProperties.DefaultTimeToLive; got != 3600 {
		t.Errorf("Read container DefaultTimeToLive = %d, want 3600", got)
	}
}

// TestSDKContainerNoDefaultTTL asserts a container created without a
// DefaultTimeToLive reads back nil (TTL stays disabled by default).
func TestSDKContainerNoDefaultTTL(t *testing.T) {
	ctx := context.Background()
	e := newCosmosEnv(t)
	cc := ttlContainer(ctx, t, e, "nottldb", "plain", nil)

	resp, err := cc.Read(ctx, nil)
	if err != nil {
		t.Fatalf("Read container: %v", err)
	}

	if resp.ContainerProperties != nil && resp.ContainerProperties.DefaultTimeToLive != nil {
		t.Errorf("Read container DefaultTimeToLive = %d, want nil (disabled)", *resp.ContainerProperties.DefaultTimeToLive)
	}
}

// TestSDKItemDefaultTTLExpires asserts a document written into a container
// with a DefaultTimeToLive is honored: present immediately after create, gone
// (404) once the injected clock advances past the TTL. The container-TTL path
// is clock-driven, so expiry is deterministic rather than wall-clock timed.
func TestSDKItemDefaultTTLExpires(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	e := newCosmosEnv(t, config.WithClock(clk))
	cc := ttlContainer(ctx, t, e, "ttlexpiredb", "sessions", to.Ptr[int32](60))

	doc := map[string]any{"id": "s1", "pk": "s1", "user": "alice"}
	createDoc(ctx, t, cc, "s1", doc)

	// Immediately after create the item is still there.
	if got := readDoc(ctx, t, cc, "s1", "s1"); got["user"] != "alice" {
		t.Errorf("pre-expiry user=%v want alice", got["user"])
	}

	clk.Advance(90 * time.Second)

	_, err := cc.ReadItem(ctx, azcosmos.NewPartitionKeyString("s1"), "s1", nil)
	wantRespErr(t, err, 404, "ReadItem after container defaultTtl elapsed")

	// A query over the container filters the expired item out too.
	pager := cc.NewQueryItemsPager("SELECT * FROM c", azcosmos.NewPartitionKeyString("s1"), nil)

	live := 0
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("NextPage: %v", perr)
		}

		live += len(page.Items)
	}

	if live != 0 {
		t.Errorf("query after expiry returned %d items, want 0", live)
	}
}

// TestSDKItemTTLOverride asserts a per-item "ttl": -1 opts a document out of
// the container's default expiry, matching real Cosmos precedence.
func TestSDKItemTTLOverride(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	e := newCosmosEnv(t, config.WithClock(clk))
	cc := ttlContainer(ctx, t, e, "ttloverridedb", "sessions", to.Ptr[int32](60))

	doc := map[string]any{"id": "keep", "pk": "keep", "user": "bob", "ttl": -1}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("keep"), b, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	clk.Advance(90 * time.Second)

	got := readDoc(ctx, t, cc, "keep", "keep")
	if got["user"] != "bob" {
		t.Errorf("item with ttl=-1 override: user=%v want bob (should not have expired)", got["user"])
	}
}

// TestSDKItemTTLDisabledOnContainer asserts an item's own "ttl" is inert when
// the container has no DefaultTimeToLive declared at all, matching real
// Cosmos: TTL must first be enabled at the container.
func TestSDKItemTTLDisabledOnContainer(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	e := newCosmosEnv(t, config.WithClock(clk))
	cc := ttlContainer(ctx, t, e, "ttldisableddb", "sessions", nil)

	doc := map[string]any{"id": "s1", "pk": "s1", "user": "alice", "ttl": 1}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := cc.CreateItem(ctx, azcosmos.NewPartitionKeyString("s1"), b, nil); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	clk.Advance(90 * time.Second)

	got := readDoc(ctx, t, cc, "s1", "s1")
	if got["user"] != "alice" {
		t.Errorf("item ttl on a TTL-disabled container: user=%v want alice (should not have expired)", got["user"])
	}
}
