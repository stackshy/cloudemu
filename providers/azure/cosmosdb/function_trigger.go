package cosmosdb

import (
	"context"
	"encoding/json"
	"strings"
)

// CosmosFunctionTriggerSink delivers a just-created-or-updated Cosmos
// document to any Azure Function whose function.json declares a
// cosmosDBTrigger input binding on the document's (databaseName,
// containerName), mirroring Cosmos's change feed. The Azure Functions
// provider implements it. It is wired per Mock instance via
// SetFunctionTriggerSink, mirroring how blobstorage.BlobFunctionTriggerSink
// wires Blob Storage delivery and servicebus.FunctionTriggerSink wires Queue
// Storage / Service Bus delivery (see providers/azure/blobstorage,
// providers/azure/servicebus).
type CosmosFunctionTriggerSink interface {
	DeliverCosmosFunctionTrigger(ctx context.Context, database, container string, body []byte)
}

// SetFunctionTriggerSink wires the Azure Functions provider as the
// destination for this Cosmos DB mock's automatic cosmosDBTrigger deliveries:
// a document created or updated asks sink to invoke any function bound to the
// document's (database, container). Real Cosmos change feed fires on
// document create and update only, never delete, so DeleteItem never calls
// dispatchFunctionTrigger. A nil sink disables delivery (the default), so any
// deployment that does not wire Azure Functions is unaffected.
func (m *Mock) SetFunctionTriggerSink(sink CosmosFunctionTriggerSink) {
	m.functionSink = sink
}

// dispatchFunctionTrigger forwards a just-written document to any Azure
// Function bound to its (database, container) by a cosmosDBTrigger binding.
// It is a no-op when no sink is wired, or when table carries no
// database/container identity to match against (see cosmosDatabaseContainer)
// — the generic driver.Database interface this mock implements has a flat
// table namespace, so a table created without going through the Cosmos SQL
// data-plane wire handler's account/database/container encoding
// (server/azure/cosmosdb's qualify) has no (database, container) pair to
// address.
//
// Called with no store lock held — PutItem/UpdateItem snapshot item and
// release m.mu before calling this — so a function invoked by the trigger may
// itself write back into Cosmos DB without deadlocking. item must already be
// a snapshot the caller owns exclusively; this method does not clone it
// again.
//
// Real Cosmos change feed delivers a BATCH of changed documents per lease
// checkpoint; this emulator delivers one document per write, wrapped in a
// single-element JSON array, an accurate enough shape for a function bound
// against the SDK's typical array-of-documents change-feed parameter, and an
// acceptable emulator simplification for synchronous per-write delivery.
// Lease container / checkpoint mechanics are not modeled.
func (m *Mock) dispatchFunctionTrigger(ctx context.Context, table string, item map[string]any) {
	if m.functionSink == nil {
		return
	}

	database, container, ok := cosmosDatabaseContainer(table)
	if !ok {
		return
	}

	body, err := json.Marshal([]map[string]any{item})
	if err != nil {
		return
	}

	m.functionSink.DeliverCosmosFunctionTrigger(ctx, database, container, body)
}

// cosmosDatabaseContainer extracts a document's (database, container) pair
// from its driver table name, mirroring how server/azure/cosmosdb's qualify
// encodes it as the table name's last two "/"-separated segments
// ("{account}/{database}/{container}", with the account segment omitted for
// the default account). Cosmos account, database and container identifiers
// cannot contain "/", so the decoding is unambiguous. A table name with fewer
// than two segments — e.g. one created directly through the flat
// driver.Database API rather than the Cosmos SQL wire layer — carries no
// database identity and returns ok=false.
func cosmosDatabaseContainer(table string) (database, container string, ok bool) {
	idx := strings.LastIndexByte(table, '/')
	if idx < 0 {
		return "", "", false
	}

	container = table[idx+1:]
	rest := table[:idx]

	database = rest
	if di := strings.LastIndexByte(rest, '/'); di >= 0 {
		database = rest[di+1:]
	}

	if database == "" || container == "" {
		return "", "", false
	}

	return database, container, true
}
