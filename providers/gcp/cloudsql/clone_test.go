package cloudsql

import (
	"context"
	"strings"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestCloneInstanceFieldsCoherent guards the field split: a clone must report
// its OWN connection name and keep a reachable IP in Endpoint — not the old bug
// where the connection-name string was written into Endpoint (corrupting the
// reported ipAddress) while ConnectionName silently inherited the source's.
func TestCloneInstanceFieldsCoherent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	src, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "source-db", Engine: "POSTGRES_15"})
	requireNoError(t, err)

	clone, err := m.CloneInstance(ctx, "source-db", "dest-db")
	requireNoError(t, err)

	if !strings.HasSuffix(clone.ConnectionName, ":dest-db") {
		t.Fatalf("clone ConnectionName = %q, want it to end in :dest-db", clone.ConnectionName)
	}

	if clone.ConnectionName == src.ConnectionName {
		t.Fatalf("clone ConnectionName must differ from source %q", src.ConnectionName)
	}

	// Endpoint stays a bare reachable IP (inherited from source's engine host),
	// never the "project:region:id" connection-name string.
	if clone.Endpoint != src.Endpoint || strings.Contains(clone.Endpoint, ":") {
		t.Fatalf("clone Endpoint = %q, want the source's reachable IP %q", clone.Endpoint, src.Endpoint)
	}
}
