package athena

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// TestRestoreNormalizesLegacySeededPrimary covers snapshots from builds that
// seeded primary with enforcement on and no output location.
func TestRestoreNormalizesLegacySeededPrimary(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	m := newMock(t)
	requireNoError(t, m.Restore(ctx, raw), "Restore")

	wg, err := m.GetWorkGroup(ctx, driver.DefaultWorkGroup)
	requireNoError(t, err, "GetWorkGroup primary")

	if e := wg.Configuration.EnforceWorkGroupConfiguration; e == nil || *e {
		t.Fatalf("restored legacy primary enforce = %v, want false", e)
	}

	qe := runQuery(t, m, driver.StartQueryExecutionInput{QueryString: "SELECT 1"})
	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("query on restored primary = %s %q", qe.Status.State, qe.Status.StateChangeReason)
	}
}

// TestRestoreKeepsModifiedPrimary leaves a primary the user changed as is.
func TestRestoreKeepsModifiedPrimary(t *testing.T) {
	ctx := context.Background()

	raw, err := os.ReadFile("testdata/legacy_databases_snapshot.json")
	requireNoError(t, err, "read fixture")

	edits := map[string]func(*driver.WorkGroup){
		"description": func(w *driver.WorkGroup) { w.Description = "mine" },
		"output location": func(w *driver.WorkGroup) {
			w.Configuration.ResultConfiguration = &driver.ResultConfiguration{OutputLocation: "s3://x/"}
		},
		"requester pays": func(w *driver.WorkGroup) { w.Configuration.RequesterPaysEnabled = ptr(true) },
		"engine version": func(w *driver.WorkGroup) {
			w.Configuration.EngineVersion = driver.EngineVersion{
				SelectedEngineVersion: "Athena engine version 2", EffectiveEngineVersion: "Athena engine version 2",
			}
		},
	}

	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			var snap map[string]json.RawMessage
			requireNoError(t, json.Unmarshal(raw, &snap), "parse fixture")

			var wgs map[string]driver.WorkGroup
			requireNoError(t, json.Unmarshal(snap["workGroups"], &wgs), "parse workGroups")

			primary := wgs[driver.DefaultWorkGroup]
			edit(&primary)
			wgs[driver.DefaultWorkGroup] = primary

			b, err := json.Marshal(wgs)
			requireNoError(t, err, "marshal workGroups")

			snap["workGroups"] = b
			data, err := json.Marshal(snap)
			requireNoError(t, err, "marshal snapshot")

			m := newMock(t)
			requireNoError(t, m.Restore(ctx, data), "Restore")

			wg, err := m.GetWorkGroup(ctx, driver.DefaultWorkGroup)
			requireNoError(t, err, "GetWorkGroup primary")

			if e := wg.Configuration.EnforceWorkGroupConfiguration; e == nil || !*e {
				t.Fatalf("modified primary enforce = %v, want true kept", e)
			}
		})
	}
}
