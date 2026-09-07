package datafusion

import (
	"context"
	"encoding/json"
	"testing"

	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

// TestSnapshotRoundTrip verifies an instance survives a Snapshot/Restore into a
// fresh mock with its identity, state, and verbatim fields intact.
func TestSnapshotRoundTrip(t *testing.T) {
	src := newMock()
	ctx := context.Background()

	_, _, err := src.CreateInstance(ctx, cfg("df", map[string]json.RawMessage{
		"type":        json.RawMessage(`"ENTERPRISE"`),
		"description": json.RawMessage(`"snap"`),
	}))
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, data))

	got, err := dst.GetInstance(ctx, "p", "us-central1", "df")
	requireNoError(t, err)

	if got.State != dfdriver.StateActive || string(got.Fields["type"]) != `"ENTERPRISE"` ||
		string(got.Fields["description"]) != `"snap"` {
		t.Fatalf("restored instance wrong: %+v", got)
	}
}
