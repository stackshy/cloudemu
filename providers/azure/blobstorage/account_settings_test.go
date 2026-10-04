package blobstorage

import (
	"context"
	"testing"
	"time"
)

func TestAccountSettingsSetGetDelete(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, ok, _ := m.AccountSetting(ctx, "st1", "fileServices"); ok {
		t.Fatal("setting present before set")
	}

	set, err := m.SetAccountSetting(ctx, "st1", "fileServices", []byte(`{"cors":{}}`))
	if err != nil {
		t.Fatal(err)
	}

	if !set.LastModified.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("LastModified = %v, want the fake clock", set.LastModified)
	}

	// Kind matches case-insensitively, and the returned bytes are a copy.
	got, ok, _ := m.AccountSetting(ctx, "st1", "FILESERVICES")
	if !ok || string(got.Properties) != `{"cors":{}}` {
		t.Fatalf("AccountSetting = %q, %v", got.Properties, ok)
	}

	got.Properties[0] = 'X'

	if again, _, _ := m.AccountSetting(ctx, "st1", "fileServices"); string(again.Properties) != `{"cors":{}}` {
		t.Errorf("stored setting mutated through a read: %q", again.Properties)
	}

	if existed, _ := m.DeleteAccountSetting(ctx, "st1", "fileServices"); !existed {
		t.Error("delete reported absent")
	}

	if existed, _ := m.DeleteAccountSetting(ctx, "st1", "fileServices"); existed {
		t.Error("second delete reported present")
	}
}

// TestDeleteStorageAccountPurgesOnlyItsSettings checks the cascade is bounded:
// deleting st1 keeps st10's settings.
func TestDeleteStorageAccountPurgesOnlyItsSettings(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, name := range []string{"st1", "st10"} {
		mustCreateAccount(t, m, name, "sub", "rg")

		if _, err := m.SetAccountSetting(ctx, name, "managementPolicies", []byte(`{"policy":{}}`)); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.DeleteStorageAccount(ctx, "st1"); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := m.AccountSetting(ctx, "st1", "managementPolicies"); ok {
		t.Error("st1 settings survived its delete")
	}

	if _, ok, _ := m.AccountSetting(ctx, "st10", "managementPolicies"); !ok {
		t.Error("deleting st1 removed st10's settings")
	}
}

func TestAccountSettingsSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.SetAccountSetting(ctx, "st1", "queueServices", []byte(`{"cors":{"corsRules":[]}}`)); err != nil {
		t.Fatal(err)
	}

	raw, err := m.Snapshot(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, raw); err != nil {
		t.Fatal(err)
	}

	got, ok, _ := restored.AccountSetting(ctx, "st1", "queueServices")
	if !ok || string(got.Properties) != `{"cors":{"corsRules":[]}}` || got.LastModified.IsZero() {
		t.Errorf("restored setting = %+v, %v", got, ok)
	}
}
