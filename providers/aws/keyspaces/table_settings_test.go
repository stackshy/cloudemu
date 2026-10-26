package keyspaces

import (
	"context"
	"encoding/json"
	"testing"

	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// The ttl and clientSideTimestamps enums hold only ENABLED, so a table that
// never enabled them records "" (not set) and GetTable omits the members. An
// explicit DISABLED is the documented default and records "" too, and once
// enabled neither setting can be turned off.
func TestTTLAndClientSideTimestampsTrackSet(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	mustKeyspace(t, m, "app")

	create := func(name, ttl, cst string) *ksdriver.Table {
		t.Helper()

		tbl, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
			KeyspaceName: "app", Name: name, SchemaDefinition: sampleSchema(),
			TTLStatus: ttl, ClientSideTimestamps: cst,
		})
		if err != nil {
			t.Fatalf("CreateTable %s: %v", name, err)
		}

		return tbl
	}

	check := func(name, wantTTL, wantCST string) {
		t.Helper()

		got, err := m.GetTable(ctx, "app", name)
		if err != nil {
			t.Fatalf("GetTable %s: %v", name, err)
		}

		if got.TTLStatus != wantTTL || got.ClientSideTimestamps != wantCST {
			t.Fatalf("%s: ttl=%q cst=%q, want ttl=%q cst=%q", name, got.TTLStatus, got.ClientSideTimestamps, wantTTL, wantCST)
		}
	}

	create("unset", "", "")
	check("unset", "", "")

	create("disabled", "DISABLED", "DISABLED")
	check("disabled", "", "")

	create("enabled", "ENABLED", "ENABLED")
	check("enabled", "ENABLED", "ENABLED")

	// Enabling later records the setting.
	if _, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "unset", TTLStatus: "ENABLED", ClientSideTimestamps: "ENABLED",
	}); err != nil {
		t.Fatalf("UpdateTable enable: %v", err)
	}

	check("unset", "ENABLED", "ENABLED")

	// DISABLED never turns an enabled setting back off.
	if _, err := m.UpdateTable(ctx, ksdriver.UpdateTableConfig{
		KeyspaceName: "app", Name: "enabled", TTLStatus: "DISABLED", ClientSideTimestamps: "DISABLED",
	}); err != nil {
		t.Fatalf("UpdateTable disable: %v", err)
	}

	check("enabled", "ENABLED", "ENABLED")
}

// A snapshot keeps whether each setting was set, and a snapshot from an older
// build, which stored DISABLED on every table, restores as not set.
func TestSnapshotTracksTTLAndClientSideTimestamps(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	mustKeyspace(t, src, "app")

	for name, status := range map[string]string{"plain": "", "on": "ENABLED"} {
		if _, err := src.CreateTable(ctx, ksdriver.CreateTableConfig{
			KeyspaceName: "app", Name: name, SchemaDefinition: sampleSchema(),
			TTLStatus: status, ClientSideTimestamps: status,
		}); err != nil {
			t.Fatalf("CreateTable %s: %v", name, err)
		}
	}

	raw, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	legacy := legacyDisabledSnapshot(t, raw)

	for label, data := range map[string]json.RawMessage{"current": raw, "legacy": legacy} {
		dst := newTestMock()
		if err := dst.Restore(ctx, data); err != nil {
			t.Fatalf("%s restore: %v", label, err)
		}

		for name, want := range map[string]string{"plain": "", "on": "ENABLED"} {
			got, err := dst.GetTable(ctx, "app", name)
			if err != nil {
				t.Fatalf("%s GetTable %s: %v", label, name, err)
			}

			if got.TTLStatus != want || got.ClientSideTimestamps != want {
				t.Fatalf("%s %s: ttl=%q cst=%q, want %q", label, name, got.TTLStatus, got.ClientSideTimestamps, want)
			}
		}
	}
}

// legacyDisabledSnapshot rewrites raw the way an older build stored it: every
// table that never set ttl or clientSideTimestamps carries DISABLED.
func legacyDisabledSnapshot(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()

	var snap keyspacesSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}

	var tables map[string]ksdriver.Table
	if err := json.Unmarshal(snap.Tables, &tables); err != nil {
		t.Fatalf("parse tables: %v", err)
	}

	for k, tbl := range tables {
		tbl.TTLStatus = disabledIfEmpty(tbl.TTLStatus)
		tbl.ClientSideTimestamps = disabledIfEmpty(tbl.ClientSideTimestamps)
		tables[k] = tbl
	}

	b, err := json.Marshal(tables)
	if err != nil {
		t.Fatalf("encode tables: %v", err)
	}

	snap.Tables = b

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	return out
}
