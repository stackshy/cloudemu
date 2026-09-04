package lambda

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

func publishTestLayer(t *testing.T, m *Mock) *driver.LayerVersion {
	t.Helper()

	lv, err := m.PublishLayerVersion(context.Background(), driver.LayerConfig{
		Name: "my-layer", Content: []byte("v1"),
	})
	requireNoError(t, err)

	return lv
}

func TestAddLayerVersionPermission(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	t.Run("success", func(t *testing.T) {
		stmtJSON, revisionID, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			StatementID: "xaccount", Action: "lambda:GetLayerVersion", Principal: "111111111111",
		}, "")
		requireNoError(t, err)
		assertNotEmpty(t, revisionID)

		if !strings.Contains(stmtJSON, "xaccount") || !strings.Contains(stmtJSON, "lambda:GetLayerVersion") {
			t.Fatalf("statement JSON = %q, want it to carry the Sid and Action", stmtJSON)
		}
	})

	t.Run("duplicate statement id", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			StatementID: "xaccount", Action: "lambda:GetLayerVersion", Principal: "222222222222",
		}, "")
		assertError(t, err, true)
	})

	t.Run("missing statement id", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			Action: "lambda:GetLayerVersion", Principal: "111111111111",
		}, "")
		assertError(t, err, true)
	})

	t.Run("missing action", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			StatementID: "s2", Principal: "111111111111",
		}, "")
		assertError(t, err, true)
	})

	t.Run("missing principal", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			StatementID: "s3", Action: "lambda:GetLayerVersion",
		}, "")
		assertError(t, err, true)
	})

	t.Run("layer not found", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "nope", 1, driver.LayerPermissionStatement{
			StatementID: "s4", Action: "lambda:GetLayerVersion", Principal: "111111111111",
		}, "")
		assertError(t, err, true)
	})

	t.Run("version not found", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", 99, driver.LayerPermissionStatement{
			StatementID: "s5", Action: "lambda:GetLayerVersion", Principal: "111111111111",
		}, "")
		assertError(t, err, true)
	})

	t.Run("revision mismatch", func(t *testing.T) {
		_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
			StatementID: "s6", Action: "lambda:GetLayerVersion", Principal: "111111111111",
		}, "not-the-real-revision")
		assertError(t, err, true)
	})
}

func TestGetLayerVersionPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	t.Run("no policy", func(t *testing.T) {
		_, _, err := m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version)
		assertError(t, err, true)
	})

	_, addedRevision, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
		StatementID: "public", Action: "lambda:GetLayerVersion", Principal: "*", OrganizationID: "o-example",
	}, "")
	requireNoError(t, err)

	t.Run("returns the policy document", func(t *testing.T) {
		policy, revisionID, gerr := m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version)
		requireNoError(t, gerr)
		assertEqual(t, addedRevision, revisionID)

		for _, want := range []string{"public", "PrincipalOrgID", "o-example", "2012-10-17"} {
			if !strings.Contains(policy, want) {
				t.Fatalf("policy = %q, want it to contain %q", policy, want)
			}
		}
	})

	t.Run("layer not found", func(t *testing.T) {
		_, _, err := m.GetLayerVersionPolicy(ctx, "nope", 1)
		assertError(t, err, true)
	})

	t.Run("version not found", func(t *testing.T) {
		_, _, err := m.GetLayerVersionPolicy(ctx, "my-layer", 99)
		assertError(t, err, true)
	})
}

func TestRemoveLayerVersionPermission(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	_, revisionID, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
		StatementID: "xaccount", Action: "lambda:GetLayerVersion", Principal: "111111111111",
	}, "")
	requireNoError(t, err)

	t.Run("revision mismatch", func(t *testing.T) {
		err := m.RemoveLayerVersionPermission(ctx, "my-layer", lv.Version, "xaccount", "stale-revision")
		assertError(t, err, true)
	})

	t.Run("statement not found", func(t *testing.T) {
		err := m.RemoveLayerVersionPermission(ctx, "my-layer", lv.Version, "nope", "")
		assertError(t, err, true)
	})

	t.Run("layer not found", func(t *testing.T) {
		err := m.RemoveLayerVersionPermission(ctx, "nope", 1, "xaccount", "")
		assertError(t, err, true)
	})

	t.Run("success", func(t *testing.T) {
		err := m.RemoveLayerVersionPermission(ctx, "my-layer", lv.Version, "xaccount", revisionID)
		requireNoError(t, err)

		_, _, err = m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version)
		assertError(t, err, true) // no statements left -> no policy
	})
}

// TestLayerVersionDeleteDropsPermissions pins that DeleteLayerVersion also
// drops the deleted version's resource policy, so a future GetLayerVersionPolicy
// on that version number correctly reports NotFound rather than a stale policy.
func TestLayerVersionDeleteDropsPermissions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
		StatementID: "s", Action: "lambda:GetLayerVersion", Principal: "111111111111",
	}, "")
	requireNoError(t, err)

	requireNoError(t, m.DeleteLayerVersion(ctx, "my-layer", lv.Version))

	_, _, err = m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version)
	assertError(t, err, true)
}

// TestAddLayerVersionPermissionCOWIndependence mutates the LayerPermissionStatement
// passed into AddLayerVersionPermission after the call returns, and independently
// mutates the returned statement JSON string's backing bytes are not shared —
// proving the stored statement is a value copy, not an alias of caller state.
func TestAddLayerVersionPermissionCOWIndependence(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	stmt := driver.LayerPermissionStatement{StatementID: "s", Action: "lambda:GetLayerVersion", Principal: "111111111111"}

	_, _, err := m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, stmt, "")
	requireNoError(t, err)

	// Mutating the local stmt after the call must not affect the stored policy.
	stmt.Principal = "mutated"

	policy, _, err := m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version)
	requireNoError(t, err)

	if strings.Contains(policy, "mutated") {
		t.Fatalf("stored policy aliases caller's statement: %q", policy)
	}
}

// TestLayerVersionPermissionConcurrentRaceFree hammers a single layer version's
// resource policy with concurrent Add/Get/RemoveLayerVersionPermission calls.
// Every one of these paths reads or mutates layerData.permissions under
// layerData.mu; without it the -race detector flags a data race. Must run clean
// under `go test -race`.
func TestLayerVersionPermissionConcurrentRaceFree(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	lv := publishTestLayer(t, m)

	const (
		workers    = 8
		iterations = 30
	)

	var wg sync.WaitGroup

	run := func(fn func()) {
		defer wg.Done()

		for i := 0; i < iterations; i++ {
			fn()
		}
	}

	wg.Add(4 * workers)

	for w := 0; w < workers; w++ {
		go run(func() {
			_, _, _ = m.AddLayerVersionPermission(ctx, "my-layer", lv.Version, driver.LayerPermissionStatement{
				StatementID: "concurrent", Action: "lambda:GetLayerVersion", Principal: "111111111111",
			}, "")
		})
		go run(func() {
			_ = m.RemoveLayerVersionPermission(ctx, "my-layer", lv.Version, "concurrent", "")
		})
		go run(func() { _, _, _ = m.GetLayerVersionPolicy(ctx, "my-layer", lv.Version) })
		go run(func() { _, _ = m.ListLayerVersions(ctx, "my-layer") })
	}

	wg.Wait()
}
