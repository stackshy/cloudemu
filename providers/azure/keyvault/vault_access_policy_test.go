package keyvault

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func apMock(t *testing.T) *Mock {
	t.Helper()

	m := New(config.NewOptions())
	if _, err := m.CreateOrUpdateVault(context.Background(), sampleVaultConfig("kv1", "sub", "rg")); err != nil {
		t.Fatal(err)
	}

	return m
}

func entry(obj, app string, secrets ...string) driver.KVAccessPolicy {
	return driver.KVAccessPolicy{TenantID: "tenant-1", ObjectID: obj, ApplicationID: app,
		Permissions: driver.KVAccessPermissions{Secrets: secrets}}
}

func findEntry(list []driver.KVAccessPolicy, obj, app string) *driver.KVAccessPolicy {
	for i := range list {
		if list[i].ObjectID == obj && list[i].ApplicationID == app {
			return &list[i]
		}
	}

	return nil
}

func TestAccessPolicyAddReplaceRemove(t *testing.T) {
	ctx := context.Background()
	m := apMock(t)
	before, _ := m.GetVault(ctx, "kv1")

	steps := []struct {
		kind    driver.KVAccessPolicyUpdateKind
		e       driver.KVAccessPolicy
		want    []string // obj-2 secrets after the step; nil means the entry is gone
		appWant []string // obj-2 with application app-1
	}{
		{driver.KVAccessPolicyAdd, entry("obj-2", "", "Get"), []string{"Get"}, nil},
		{driver.KVAccessPolicyAdd, entry("obj-2", "", "get", "List"), []string{"Get", "List"}, nil},
		{driver.KVAccessPolicyAdd, entry("obj-2", "app-1", "Set"), []string{"Get", "List"}, []string{"Set"}},
		{driver.KVAccessPolicyReplace, entry("obj-2", "", "Delete"), []string{"Delete"}, []string{"Set"}},
		{driver.KVAccessPolicyRemove, entry("obj-2", "app-1", "set"), []string{"Delete"}, nil},
		{driver.KVAccessPolicyRemove, entry("obj-2", "", "Delete"), nil, nil},
	}

	for i, st := range steps {
		list, err := m.UpdateVaultAccessPolicies(ctx, "kv1", st.kind, []driver.KVAccessPolicy{st.e})
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		for _, c := range []struct {
			app  string
			want []string
		}{{"", st.want}, {"app-1", st.appWant}} {
			got := findEntry(list, "obj-2", c.app)
			if (got == nil) != (c.want == nil) || (got != nil && !reflect.DeepEqual(got.Permissions.Secrets, c.want)) {
				t.Errorf("step %d app %q: got %+v, want secrets %v", i, c.app, got, c.want)
			}
		}
	}

	after, _ := m.GetVault(ctx, "kv1")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("vault changed by add/remove round trip:\nbefore %+v\nafter  %+v", before, after)
	}

	if _, err := m.UpdateVaultAccessPolicies(ctx, "nope", driver.KVAccessPolicyAdd, nil); !cerrors.IsNotFound(err) {
		t.Errorf("missing vault err = %v", err)
	}

	if _, err := m.UpdateVaultAccessPolicies(ctx, "kv1", "merge", nil); !cerrors.IsInvalidArgument(err) {
		t.Errorf("bad kind err = %v", err)
	}
}

func TestUpdateVaultKeepsPatchInvariants(t *testing.T) {
	ctx := context.Background()
	m := apMock(t)
	before, _ := m.GetVault(ctx, "kv1")

	off := false

	got, err := m.UpdateVault(ctx, "kv1", func(v *driver.KVVaultInfo) error {
		v.Tags = map[string]string{"env": "prod"}
		v.Properties.EnableSoftDelete = &off

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !*got.Properties.EnableSoftDelete || got.Properties.VaultURI != before.Properties.VaultURI ||
		got.Properties.SKU != before.Properties.SKU || got.Properties.TenantID != before.Properties.TenantID ||
		!reflect.DeepEqual(got.Properties.AccessPolicies, before.Properties.AccessPolicies) {
		t.Errorf("PATCH broke an invariant: %+v", got.Properties)
	}

	got.Tags["env"] = "mutated"
	if stored, _ := m.GetVault(ctx, "kv1"); stored.Tags["env"] != "prod" {
		t.Error("UpdateVault returned the stored record, not a copy")
	}

	_, err = m.UpdateVault(ctx, "kv1", func(v *driver.KVVaultInfo) error {
		v.Tags = nil
		return cerrors.New(cerrors.NotFound, "wrong resource group")
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("mutate error = %v", err)
	}

	if stored, _ := m.GetVault(ctx, "kv1"); stored.Tags["env"] != "prod" {
		t.Error("a failed mutate changed the vault")
	}
}

// TestAccessPolicyAddsAreAtomic: each goroutine adds a distinct principal; a
// lost update shows up as fewer than workers entries.
func TestAccessPolicyAddsAreAtomic(t *testing.T) {
	const workers = 200

	ctx := context.Background()
	m := apMock(t)
	base := len(sampleVaultConfig("kv1", "sub", "rg").Properties.AccessPolicies)

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyAdd,
				[]driver.KVAccessPolicy{entry(fmt.Sprintf("p-%d", i), "", "Get")}); err != nil {
				t.Error(err)
			}
		}()
	}

	wg.Wait()

	v, _ := m.GetVault(ctx, "kv1")
	if got := len(v.Properties.AccessPolicies); got != base+workers {
		t.Errorf("entries = %d, want %d", got, base+workers)
	}
}

// TestAccessPolicyAddsSurviveConcurrentPatches interleaves adds with tag
// PATCHes; neither may overwrite the other.
func TestAccessPolicyAddsSurviveConcurrentPatches(t *testing.T) {
	const n = 100

	ctx := context.Background()
	m := apMock(t)
	base := len(sampleVaultConfig("kv1", "sub", "rg").Properties.AccessPolicies)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(2)

		go func() {
			defer wg.Done()

			if _, err := m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyAdd,
				[]driver.KVAccessPolicy{entry(fmt.Sprintf("p-%d", i), "", "Get")}); err != nil {
				t.Error(err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.UpdateVault(ctx, "kv1", func(v *driver.KVVaultInfo) error {
				v.Tags = map[string]string{"final": "yes"}
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}

	wg.Wait()

	v, _ := m.GetVault(ctx, "kv1")
	if len(v.Properties.AccessPolicies) != base+n || v.Tags["final"] != "yes" {
		t.Errorf("entries = %d (want %d), tags = %v", len(v.Properties.AccessPolicies), base+n, v.Tags)
	}
}

func TestAccessPolicyApplicationIDSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	m := apMock(t)

	if _, err := m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyAdd,
		[]driver.KVAccessPolicy{entry("obj-9", "app-9", "Get")}); err != nil {
		t.Fatal(err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	r := New(config.NewOptions())
	if err := r.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	v, _ := r.GetVault(ctx, "kv1")
	if findEntry(v.Properties.AccessPolicies, "obj-9", "app-9") == nil {
		t.Errorf("applicationId lost on restore: %+v", v.Properties.AccessPolicies)
	}
}

func TestAccessPolicyRemoveByIdentityAndReplaceAbsent(t *testing.T) {
	ctx := context.Background()
	m := apMock(t)

	if _, err := m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyAdd,
		[]driver.KVAccessPolicy{entry("obj-3", "", "Get", "List")}); err != nil {
		t.Fatal(err)
	}

	list, err := m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyRemove,
		[]driver.KVAccessPolicy{entry("obj-3", "")})
	if err != nil || findEntry(list, "obj-3", "") != nil {
		t.Fatalf("remove by identity: err=%v entry=%+v, want entry gone", err, findEntry(list, "obj-3", ""))
	}

	before, _ := m.GetVault(ctx, "kv1")

	_, err = m.UpdateVaultAccessPolicies(ctx, "kv1", driver.KVAccessPolicyReplace,
		[]driver.KVAccessPolicy{entry("obj-absent", "", "Get")})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("replace of absent principal err = %v, want NotFound", err)
	}

	if after, _ := m.GetVault(ctx, "kv1"); !reflect.DeepEqual(before, after) {
		t.Errorf("failed replace changed the vault")
	}
}
