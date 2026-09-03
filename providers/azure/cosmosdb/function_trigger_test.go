package cosmosdb

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCosmosSink records every DeliverCosmosFunctionTrigger call it observes,
// standing in for the Azure Functions provider in tests that only exercise
// the Cosmos DB mock's dispatch, not real function matching/invocation.
type fakeCosmosSink struct {
	mu    sync.Mutex
	calls []cosmosSinkCall
}

type cosmosSinkCall struct {
	database  string
	container string
	body      string
}

func (f *fakeCosmosSink) DeliverCosmosFunctionTrigger(_ context.Context, database, container string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, cosmosSinkCall{database: database, container: container, body: string(body)})
}

func (f *fakeCosmosSink) snapshot() []cosmosSinkCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]cosmosSinkCall(nil), f.calls...)
}

func TestPutItemDispatchesCosmosFunctionTrigger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	sink := &fakeCosmosSink{}
	m.SetFunctionTriggerSink(sink)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct/orders-db/orders", PartitionKey: "id"}))

	require.NoError(t, m.PutItem(ctx, "acct/orders-db/orders", map[string]any{"id": "1", "status": "new"}))

	calls := sink.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "orders-db", calls[0].database)
	assert.Equal(t, "orders", calls[0].container)
	assert.JSONEq(t, `[{"id":"1","status":"new"}]`, calls[0].body)
}

func TestUpdateItemDispatchesCosmosFunctionTrigger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	sink := &fakeCosmosSink{}
	m.SetFunctionTriggerSink(sink)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct/orders-db/orders", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "acct/orders-db/orders", map[string]any{"id": "1", "status": "new"}))

	_, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "acct/orders-db/orders",
		Key:   map[string]any{"id": "1"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "status", Value: "shipped"},
		},
	})
	require.NoError(t, err)

	calls := sink.snapshot()
	require.Len(t, calls, 2, "PutItem then UpdateItem should each dispatch once")
	assert.Equal(t, "orders-db", calls[1].database)
	assert.Equal(t, "orders", calls[1].container)
	assert.JSONEq(t, `[{"id":"1","status":"shipped"}]`, calls[1].body)
}

func TestDeleteItemDoesNotDispatchCosmosFunctionTrigger(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	sink := &fakeCosmosSink{}
	m.SetFunctionTriggerSink(sink)

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct/orders-db/orders", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "acct/orders-db/orders", map[string]any{"id": "1"}))
	require.NoError(t, m.DeleteItem(ctx, "acct/orders-db/orders", map[string]any{"id": "1"}))

	calls := sink.snapshot()
	require.Len(t, calls, 1, "only the PutItem should have dispatched; delete must not fire a cosmosDBTrigger")
}

func TestPutItemNoDispatchWithoutSink(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct/orders-db/orders", PartitionKey: "id"}))

	// A nil sink (the default) must not panic and must simply skip delivery.
	require.NoError(t, m.PutItem(ctx, "acct/orders-db/orders", map[string]any{"id": "1"}))
}

func TestPutItemNoDispatchForUnqualifiedTable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	sink := &fakeCosmosSink{}
	m.SetFunctionTriggerSink(sink)

	// A table created directly through the flat driver.Database API (no
	// account/database/container encoding) carries no database identity.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "flat-table", PartitionKey: "id"}))
	require.NoError(t, m.PutItem(ctx, "flat-table", map[string]any{"id": "1"}))

	assert.Empty(t, sink.snapshot())
}

func TestCosmosDatabaseContainer(t *testing.T) {
	tests := []struct {
		name          string
		table         string
		wantDatabase  string
		wantContainer string
		wantOK        bool
	}{
		{name: "account qualified", table: "acct/db1/coll1", wantDatabase: "db1", wantContainer: "coll1", wantOK: true},
		{name: "default account", table: "db1/coll1", wantDatabase: "db1", wantContainer: "coll1", wantOK: true},
		{name: "no separator", table: "flat", wantOK: false},
		{name: "empty", table: "", wantOK: false},
		{name: "trailing slash", table: "db1/", wantOK: false},
		{name: "leading slash", table: "/coll1", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, container, ok := cosmosDatabaseContainer(tt.table)
			assert.Equal(t, tt.wantOK, ok)

			if tt.wantOK {
				assert.Equal(t, tt.wantDatabase, db)
				assert.Equal(t, tt.wantContainer, container)
			}
		})
	}
}
