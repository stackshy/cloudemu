package functions

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
)

// cosmosDBTriggerType is the function.json trigger type a cosmosDBTrigger
// binding declares, as stamped by the ARM CreateFunction body's
// "config.bindings".
const cosmosDBTriggerType = "cosmosDBTrigger"

// DeliverCosmosFunctionTrigger invokes every deployed function whose
// function.json declares a cosmosDBTrigger input binding on (database,
// container), passing body (the changed document, wrapped in a JSON array by
// the caller) as the invocation payload. It is the cross-service seam the
// Azure Cosmos DB mock calls after a successful document create/update (see
// providers/azure/cosmosdb's CosmosFunctionTriggerSink), mirroring
// DeliverBlobFunctionTrigger for Blob Storage.
//
// Real Azure's cosmosDBTrigger (Cosmos change feed) fires on document create
// and update, never on delete, so the Cosmos DB mock only calls this from its
// write paths. Delivery is synchronous and recursion-guarded exactly like the
// other triggers: a function that writes back into its own monitored
// container would otherwise invoke itself unbounded.
func (m *Mock) DeliverCosmosFunctionTrigger(ctx context.Context, database, container string, body []byte) {
	if database == "" || container == "" {
		return
	}

	if recursionguard.Depth(ctx) >= recursionguard.MaxDepth {
		return
	}

	match := func(b map[string]any) bool {
		dn, _ := b["databaseName"].(string)
		cn, _ := b["containerName"].(string)

		return dn == database && cn == container
	}

	for _, app := range m.appsBoundBy(cosmosDBTriggerType, match) {
		_ = m.InvokeExternal(ctx, app, body)
	}
}
