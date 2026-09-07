package dataform_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/dataform"
	dfdriver "github.com/stackshy/cloudemu/v2/services/dataform/driver"
)

func newMock() *dataform.Mock {
	return dataform.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func baseConfig() *dfdriver.RepositoryConfig {
	return &dfdriver.RepositoryConfig{
		Project: "proj", Location: "us-central1", ID: "repo1",
		DisplayName:       "My Repo",
		Labels:            map[string]string{"env": "dev"},
		GitRemoteSettings: json.RawMessage(`{"url":"https://github.com/x/y.git","defaultBranch":"main"}`),
		WorkspaceCompilationOverrides: json.RawMessage(
			`{"defaultDatabase":"db","schemaSuffix":"_dev","tablePrefix":"tp_"}`),
	}
}

func TestCreateGetRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	created, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	assertEqual(t, created.ID, "repo1")

	if created.CreateTime == "" {
		t.Fatal("createTime not minted")
	}

	got, err := m.GetRepository(ctx, "proj", "us-central1", "repo1")
	requireNoError(t, err)

	assertEqual(t, got.DisplayName, "My Repo")
	assertEqual(t, string(got.GitRemoteSettings), string(baseConfig().GitRemoteSettings))
	assertEqual(t, string(got.WorkspaceCompilationOverrides), string(baseConfig().WorkspaceCompilationOverrides))

	// createTime is stable across reads.
	assertEqual(t, got.CreateTime, created.CreateTime)
}

func TestCreateDuplicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	_, err = m.CreateRepository(ctx, baseConfig())
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.GetRepository(context.Background(), "proj", "us-central1", "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestCreateRequiresID(t *testing.T) {
	m := newMock()
	cfg := baseConfig()
	cfg.ID = ""

	_, err := m.CreateRepository(context.Background(), cfg)
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestPatchMaskedReplacesOnlyListedFields(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	// Patch only displayName; git settings must survive.
	patch := &dfdriver.RepositoryConfig{
		Project: "proj", Location: "us-central1", ID: "repo1",
		DisplayName: "Renamed",
	}

	got, err := m.PatchRepository(ctx, patch, []string{"displayName"})
	requireNoError(t, err)

	assertEqual(t, got.DisplayName, "Renamed")
	assertEqual(t, string(got.GitRemoteSettings), string(baseConfig().GitRemoteSettings))

	if got.DisplayName == "" || len(got.Labels) != 1 {
		t.Fatalf("unmentioned fields not preserved: %+v", got)
	}
}

func TestListScopedToLocation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	other := baseConfig()
	other.ID = "repo2"
	other.Location = "europe-west1"
	_, err = m.CreateRepository(ctx, other)
	requireNoError(t, err)

	list, err := m.ListRepositories(ctx, "proj", "us-central1")
	requireNoError(t, err)

	if len(list) != 1 || list[0].ID != "repo1" {
		t.Fatalf("expected only repo1 in us-central1, got %+v", list)
	}
}

func TestDeleteThenNotFound(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	requireNoError(t, m.DeleteRepository(ctx, "proj", "us-central1", "repo1"))

	if err := m.DeleteRepository(ctx, "proj", "us-central1", "repo1"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound on second delete, got %v", err)
	}
}

func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, snap))

	got, err := restored.GetRepository(ctx, "proj", "us-central1", "repo1")
	requireNoError(t, err)

	assertEqual(t, got.DisplayName, "My Repo")
	assertEqual(t, string(got.GitRemoteSettings), string(baseConfig().GitRemoteSettings))
}

// TestReadsAreIsolated proves a mutation of a returned repository's raw block
// does not alias stored state.
func TestReadsAreIsolated(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRepository(ctx, baseConfig())
	requireNoError(t, err)

	got, err := m.GetRepository(ctx, "proj", "us-central1", "repo1")
	requireNoError(t, err)

	got.GitRemoteSettings[0] = 'X'
	got.Labels["env"] = "prod"

	fresh, err := m.GetRepository(ctx, "proj", "us-central1", "repo1")
	requireNoError(t, err)

	assertEqual(t, string(fresh.GitRemoteSettings), string(baseConfig().GitRemoteSettings))
	assertEqual(t, fresh.Labels["env"], "dev")
}
