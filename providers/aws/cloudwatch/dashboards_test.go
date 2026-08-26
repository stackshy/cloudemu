package cloudwatch

import (
	"context"
	"testing"
)

func TestPutGetDashboard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	body := `{"widgets":[{"type":"metric","x":0,"y":0}]}`

	t.Run("put valid then get", func(t *testing.T) {
		requireNoError(t, m.PutDashboard(ctx, "ops", body))

		d, err := m.GetDashboard(ctx, "ops")
		requireNoError(t, err)
		assertEqual(t, "ops", d.Name)
		assertEqual(t, body, d.Body)
		assertEqual(t, len(body), d.Size)
		if d.ARN == "" {
			t.Fatal("expected a non-empty dashboard ARN")
		}
		if d.LastModified.IsZero() {
			t.Fatal("expected a non-zero LastModified")
		}
	})

	t.Run("put invalid JSON is rejected", func(t *testing.T) {
		assertError(t, m.PutDashboard(ctx, "bad", "not-json"), true)
	})

	t.Run("empty name is rejected", func(t *testing.T) {
		assertError(t, m.PutDashboard(ctx, "", body), true)
	})

	t.Run("get unknown is not found", func(t *testing.T) {
		_, err := m.GetDashboard(ctx, "missing")
		assertError(t, err, true)
	})

	t.Run("put overwrites existing", func(t *testing.T) {
		newBody := `{"widgets":[]}`
		requireNoError(t, m.PutDashboard(ctx, "ops", newBody))

		d, err := m.GetDashboard(ctx, "ops")
		requireNoError(t, err)
		assertEqual(t, newBody, d.Body)
	})
}

func TestListDashboards(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	body := `{}`
	requireNoError(t, m.PutDashboard(ctx, "prod-web", body))
	requireNoError(t, m.PutDashboard(ctx, "prod-api", body))
	requireNoError(t, m.PutDashboard(ctx, "staging-web", body))

	t.Run("no prefix lists all sorted", func(t *testing.T) {
		entries, err := m.ListDashboards(ctx, "")
		requireNoError(t, err)
		assertEqual(t, 3, len(entries))
		assertEqual(t, "prod-api", entries[0].Name)
		assertEqual(t, "prod-web", entries[1].Name)
		assertEqual(t, "staging-web", entries[2].Name)
	})

	t.Run("prefix filters", func(t *testing.T) {
		entries, err := m.ListDashboards(ctx, "prod-")
		requireNoError(t, err)
		assertEqual(t, 2, len(entries))
		assertEqual(t, "prod-api", entries[0].Name)
		assertEqual(t, "prod-web", entries[1].Name)
	})

	t.Run("entries carry arn and size", func(t *testing.T) {
		entries, err := m.ListDashboards(ctx, "staging-web")
		requireNoError(t, err)
		assertEqual(t, 1, len(entries))
		assertEqual(t, len(body), entries[0].Size)
		if entries[0].ARN == "" {
			t.Fatal("expected a non-empty ARN on the list entry")
		}
	})
}

func TestDeleteDashboards(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.PutDashboard(ctx, "a", `{}`))
	requireNoError(t, m.PutDashboard(ctx, "b", `{}`))

	t.Run("deletes each named", func(t *testing.T) {
		requireNoError(t, m.DeleteDashboards(ctx, []string{"a", "b"}))

		_, err := m.GetDashboard(ctx, "a")
		assertError(t, err, true)
		_, err = m.GetDashboard(ctx, "b")
		assertError(t, err, true)
	})

	t.Run("unknown name is not found and deletes none", func(t *testing.T) {
		requireNoError(t, m.PutDashboard(ctx, "keep", `{}`))

		err := m.DeleteDashboards(ctx, []string{"keep", "gone"})
		assertError(t, err, true)

		// all-or-nothing: "keep" must still exist.
		_, err = m.GetDashboard(ctx, "keep")
		requireNoError(t, err)
	})
}
