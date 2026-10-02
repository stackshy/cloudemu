package containerapps_test

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
)

func mustNamedEnv(t *testing.T, m *containerapps.Mock, name string) {
	t.Helper()

	if _, _, err := m.CreateOrUpdateEnvironment(context.Background(), sub, rg, name,
		containerapps.EnvironmentInput{Location: region}); err != nil {
		t.Fatal(err)
	}
}

func mustChildren(t *testing.T, m *containerapps.Mock, env string) {
	t.Helper()

	ctx := context.Background()

	if _, err := m.PutDaprComponent(ctx, sub, rg, env, &containerapps.DaprComponent{
		Name: "d1", ComponentType: "state.redis", Version: "v1",
		Secrets: []containerapps.DaprSecret{{Name: "k", Value: "v"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PutEnvStorage(ctx, sub, rg, env, &containerapps.EnvStorage{
		Name: "s1", AzureFile: &containerapps.AzureFileStorage{
			AccountName: "a", AccountKey: "k", ShareName: "sh", AccessMode: "ReadWrite",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func childCount(t *testing.T, m *containerapps.Mock, env string) int {
	t.Helper()

	ctx := context.Background()
	n := 0

	if _, err := m.GetDaprComponent(ctx, sub, rg, env, "d1"); err == nil {
		n++
	}

	if _, err := m.GetEnvStorage(ctx, sub, rg, env, "s1"); err == nil {
		n++
	}

	return n
}

func TestEnvChildrenValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, err := m.PutDaprComponent(ctx, sub, rg, "missing", &containerapps.DaprComponent{
		Name: "d1", ComponentType: "state.redis", Version: "v1",
	}); !cerrors.IsNotFound(err) {
		t.Errorf("dapr under a missing env: %v, want NotFound", err)
	}

	mustNamedEnv(t, m, envNm)

	cases := []struct {
		name string
		put  func() error
	}{
		{"dapr without version", func() error {
			_, err := m.PutDaprComponent(ctx, sub, rg, envNm, &containerapps.DaprComponent{Name: "d", ComponentType: "x"})
			return err
		}},
		{"storage bad accessMode", func() error {
			_, err := m.PutEnvStorage(ctx, sub, rg, envNm, &containerapps.EnvStorage{Name: "s",
				AzureFile: &containerapps.AzureFileStorage{AccountName: "a", ShareName: "s", AccessMode: "Write"}})
			return err
		}},
		{"storage without azureFile", func() error {
			_, err := m.PutEnvStorage(ctx, sub, rg, envNm, &containerapps.EnvStorage{Name: "s"})
			return err
		}},
	}

	for _, tc := range cases {
		if err := tc.put(); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s: %v, want InvalidArgument", tc.name, err)
		}
	}
}

// TestEnvDeleteCascadeIsBounded checks deleting env1 removes its children and
// keeps env10's.
func TestEnvDeleteCascadeIsBounded(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, env := range []string{"env1", "env10"} {
		mustNamedEnv(t, m, env)
		mustChildren(t, m, env)
	}

	if _, err := m.DeleteEnvironment(ctx, sub, rg, "env1"); err != nil {
		t.Fatal(err)
	}

	if n := childCount(t, m, "env1"); n != 0 {
		t.Errorf("env1 kept %d children after delete", n)
	}

	if n := childCount(t, m, "env10"); n != 2 {
		t.Errorf("env10 has %d children after env1 delete, want 2", n)
	}

	if err := m.PurgeResourceGroup(ctx, sub, rg); err != nil {
		t.Fatal(err)
	}

	if n := childCount(t, m, "env10"); n != 0 {
		t.Errorf("env10 kept %d children after RG purge", n)
	}
}

func TestEnvChildrenSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	mustNamedEnv(t, m, envNm)
	mustChildren(t, m, envNm)

	raw, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, raw); err != nil {
		t.Fatal(err)
	}

	c, err := restored.GetDaprComponent(ctx, sub, rg, envNm, "d1")
	if err != nil || c.Name != "d1" || c.Version != "v1" || len(c.Secrets) != 1 || c.Secrets[0].Value != "v" {
		t.Errorf("restored dapr = %+v, %v", c, err)
	}

	s, err := restored.ListEnvStorages(ctx, sub, rg, envNm)
	if err != nil || len(s) != 1 || s[0].Name != "s1" || s[0].AzureFile.AccountKey != "k" {
		t.Errorf("restored storages = %+v, %v", s, err)
	}
}
