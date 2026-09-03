package functions

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
)

// blobTriggerType is the function.json trigger type a blobTrigger binding
// declares, as stamped by the ARM CreateFunction body's "config.bindings".
const blobTriggerType = "blobTrigger"

// DeliverBlobFunctionTrigger invokes every deployed function whose
// function.json declares a blobTrigger input binding whose path names
// container, passing the written blob's content as the invocation payload. It
// is the cross-service seam the Azure Blob Storage mock calls after a
// successful blob create/update (see providers/azure/blobstorage's
// BlobFunctionTriggerSink), mirroring DeliverFunctionTrigger for Queue
// Storage / Service Bus.
//
// Real Azure's blobTrigger fires on blob create and update, never on delete,
// so the Blob Storage mock only calls this from its write paths. Delivery is
// synchronous and recursion-guarded exactly like DeliverFunctionTrigger: a
// function that writes back into its own trigger container would otherwise
// invoke itself unbounded.
func (m *Mock) DeliverBlobFunctionTrigger(ctx context.Context, container, blobName string, body []byte) {
	if container == "" {
		return
	}

	if recursionguard.Depth(ctx) >= recursionguard.MaxDepth {
		return
	}

	match := func(b map[string]any) bool {
		path, _ := b["path"].(string)
		return blobTriggerPathMatches(path, container, blobName)
	}

	for _, app := range m.appsBoundBy(blobTriggerType, match) {
		_ = m.InvokeExternal(ctx, app, body)
	}
}

// blobTriggerPathMatches reports whether a blobTrigger binding's path (e.g.
// "samples-workitems/{name}" or "samples-workitems/logs/{name}.txt") matches
// a written blob: the path's first segment must equal container, and when a
// second segment is present, its literal prefix before the first "{" token
// (if any) must prefix blobName. A bare container path, or one whose second
// segment is entirely a token, matches every blob in the container.
//
// This covers the container match Azure always enforces; it does not evaluate
// the full token-capture grammar (e.g. "{name}.{ext}" multi-token patterns),
// which is deferred as binding-data extraction is not needed for delivery.
func blobTriggerPathMatches(path, container, blobName string) bool {
	path = strings.TrimPrefix(path, "/")

	head, rest, hasRest := strings.Cut(path, "/")
	if head != container {
		return false
	}

	if !hasRest {
		return true
	}

	if idx := strings.IndexByte(rest, '{'); idx >= 0 {
		rest = rest[:idx]
	}

	return strings.HasPrefix(blobName, rest)
}
