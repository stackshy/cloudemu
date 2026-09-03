// Package gcp_test — suite cell STORAGE / gcp / sdk-compat lifecycle conditions.
//
// Drives the real cloud.google.com/go/storage SDK against the emulator to
// prove the full GCS lifecycle rule condition set (numNewerVersions, isLive,
// createdBefore, matchesStorageClass, matchesPrefix/Suffix, age) round-trips
// through Create/Update -> Attrs, rather than collapsing to age-only.
package gcs_test

import (
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

// TestLifecycleConditionsRoundTrip sets a lifecycle whose rules exercise the
// versioning-aware and date/class conditions, then reads them back and asserts
// every condition survives.
func TestLifecycleConditionsRoundTrip(t *testing.T) {
	ctx, client := newStorageClient(t)

	created := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	attrs := &storage.BucketAttrs{
		VersioningEnabled: true,
		Lifecycle: storage.Lifecycle{Rules: []storage.LifecycleRule{
			{
				Action: storage.LifecycleAction{Type: storage.DeleteAction},
				Condition: storage.LifecycleCondition{
					NumNewerVersions: 3,
					Liveness:         storage.Archived,
					CreatedBefore:    created,
				},
			},
			{
				Action: storage.LifecycleAction{Type: storage.SetStorageClassAction, StorageClass: "COLDLINE"},
				Condition: storage.LifecycleCondition{
					AgeInDays:             60,
					Liveness:              storage.Live,
					MatchesStorageClasses: []string{"STANDARD", "NEARLINE"},
					MatchesPrefix:         []string{"logs/"},
					MatchesSuffix:         []string{".log"},
				},
			},
		}},
	}

	b := client.Bucket("e2e-lc-conditions")
	if err := b.Create(ctx, e2eProject, attrs); err != nil {
		t.Fatalf("Create bucket with lifecycle: %v", err)
	}

	got, err := b.Attrs(ctx)
	if err != nil {
		t.Fatalf("bucket Attrs: %v", err)
	}

	if len(got.Lifecycle.Rules) != 2 {
		t.Fatalf("lifecycle rules = %d, want 2 (conditions dropped?): %+v", len(got.Lifecycle.Rules), got.Lifecycle.Rules)
	}

	r0 := got.Lifecycle.Rules[0]
	if r0.Action.Type != storage.DeleteAction {
		t.Errorf("rule0 action = %q, want Delete", r0.Action.Type)
	}

	if r0.Condition.NumNewerVersions != 3 {
		t.Errorf("rule0 NumNewerVersions = %d, want 3", r0.Condition.NumNewerVersions)
	}

	if r0.Condition.Liveness != storage.Archived {
		t.Errorf("rule0 Liveness = %v, want Archived (isLive:false)", r0.Condition.Liveness)
	}

	if !r0.Condition.CreatedBefore.Equal(created) {
		t.Errorf("rule0 CreatedBefore = %v, want %v", r0.Condition.CreatedBefore, created)
	}

	r1 := got.Lifecycle.Rules[1]
	if r1.Action.Type != storage.SetStorageClassAction || r1.Action.StorageClass != "COLDLINE" {
		t.Errorf("rule1 action = %+v, want SetStorageClass COLDLINE", r1.Action)
	}

	if r1.Condition.AgeInDays != 60 {
		t.Errorf("rule1 AgeInDays = %d, want 60", r1.Condition.AgeInDays)
	}

	if r1.Condition.Liveness != storage.Live {
		t.Errorf("rule1 Liveness = %v, want Live (isLive:true)", r1.Condition.Liveness)
	}

	if !equalStrings(r1.Condition.MatchesStorageClasses, []string{"STANDARD", "NEARLINE"}) {
		t.Errorf("rule1 MatchesStorageClasses = %v, want [STANDARD NEARLINE]", r1.Condition.MatchesStorageClasses)
	}

	if !equalStrings(r1.Condition.MatchesPrefix, []string{"logs/"}) {
		t.Errorf("rule1 MatchesPrefix = %v, want [logs/]", r1.Condition.MatchesPrefix)
	}

	if !equalStrings(r1.Condition.MatchesSuffix, []string{".log"}) {
		t.Errorf("rule1 MatchesSuffix = %v, want [.log]", r1.Condition.MatchesSuffix)
	}
}

// TestLifecycleConditionsUpdate confirms conditions also survive the PATCH
// (Bucket.Update) path, not just Create.
func TestLifecycleConditionsUpdate(t *testing.T) {
	ctx, client := newStorageClient(t)
	b := mustCreateBucket(t, ctx, client, "e2e-lc-update")

	lc := storage.Lifecycle{Rules: []storage.LifecycleRule{{
		Action: storage.LifecycleAction{Type: storage.DeleteAction},
		Condition: storage.LifecycleCondition{
			NumNewerVersions: 5,
			MatchesPrefix:    []string{"tmp/"},
		},
	}}}

	if _, err := b.Update(ctx, storage.BucketAttrsToUpdate{Lifecycle: &lc}); err != nil {
		t.Fatalf("bucket Update(Lifecycle): %v", err)
	}

	got, err := b.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after update: %v", err)
	}

	if len(got.Lifecycle.Rules) != 1 {
		t.Fatalf("lifecycle rules = %d, want 1", len(got.Lifecycle.Rules))
	}

	if got.Lifecycle.Rules[0].Condition.NumNewerVersions != 5 {
		t.Errorf("NumNewerVersions = %d, want 5 (dropped on PATCH?)", got.Lifecycle.Rules[0].Condition.NumNewerVersions)
	}

	if !equalStrings(got.Lifecycle.Rules[0].Condition.MatchesPrefix, []string{"tmp/"}) {
		t.Errorf("MatchesPrefix = %v, want [tmp/]", got.Lifecycle.Rules[0].Condition.MatchesPrefix)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
