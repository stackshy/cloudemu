package persist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/persist"
)

// TestSnapshotServicesDiscovery checks the auto-discovery contract without a
// frozen golden list: for each provider factory, SnapshotServices() (which calls
// snapshot.Discover) must return exactly the set of keys an INDEPENDENT
// reflection over the concrete Provider computes.
//
// The expected set is computed by reflectSnapshotKeys, a separate reimplementation
// of the contract (exported, non-nil fields whose type implements Snapshottable,
// keyed by the lowercased field name). It deliberately does NOT call Discover, so
// the two only agree when Discover is correct. This still fails if Discover:
//   - skips a field that implements Snapshottable (missed service),
//   - returns a key for a field that does not implement it,
//   - derives the key with the wrong casing, or
//   - silently collapses two fields onto one key (collision).
//
// Because both sides recompute from the live struct, a NEW Snapshottable service
// grows both sets together and needs no edit here — that is the point: parallel
// persist waves never touch this file.
func TestSnapshotServicesDiscovery(t *testing.T) {
	tests := []struct {
		provider string
		p        snapshotProvider
	}{
		{"aws", cloudemu.NewAWS()},
		{"azure", cloudemu.NewAzure()},
		{"gcp", cloudemu.NewGCP()},
		{"oci", cloudemu.NewOCI()},
	}

	for _, tc := range tests {
		discovered := tc.p.SnapshotServices()
		got := asSet(keysOf(discovered))

		want, collisions := reflectSnapshotKeys(tc.p)
		if len(collisions) != 0 {
			t.Errorf("%s: independent reflection found colliding snapshot keys %v — two Provider fields lowercase to the same key",
				tc.provider, collisions)
		}

		if g, w := sortedSet(got), sortedSet(want); strings.Join(g, ",") != strings.Join(w, ",") {
			t.Errorf("%s SnapshotServices keys = %v, want (independently computed) %v", tc.provider, g, w)
		}

		for k := range got {
			if k != strings.ToLower(k) {
				t.Errorf("%s key %q is not lowercased (keys must be stable, case-normalized)", tc.provider, k)
			}
		}

		// A discovered value must never be nil — Snapshot would panic on it.
		for name, s := range discovered {
			if s == nil {
				t.Errorf("%s service %q discovered as nil", tc.provider, name)
			}
		}
	}
}

// snapshotProvider is the slice of a provider factory the discovery test needs:
// the auto-discovery entry point. Each of NewAWS/NewAzure/NewGCP/NewOCI satisfies
// it. reflectSnapshotKeys still reflects over the concrete value behind it.
type snapshotProvider interface {
	SnapshotServices() persist.Services
}

// TestSnapshotDiscoveryCatchesRegressions is the negative path: it proves the
// property assertion above would actually fire on a broken discovery, using
// hand-built fake providers whose expected behavior is known.
func TestSnapshotDiscoveryCatchesRegressions(t *testing.T) {
	// A missed service: reflectSnapshotKeys includes a Snapshottable field; a
	// hypothetical Discover that dropped it would produce a smaller set, and the
	// set comparison (via setsEqual) would report inequality.
	type provider struct {
		Alpha   fakeSnapshottable // implements -> "alpha"
		Beta    fakeSnapshottable // implements -> "beta"
		NotASvc int               // not Snapshottable -> ignored
	}

	keys, collisions := reflectSnapshotKeys(&provider{})
	if len(collisions) != 0 {
		t.Fatalf("unexpected collisions on clean fake provider: %v", collisions)
	}

	if want := map[string]struct{}{"alpha": {}, "beta": {}}; !setsEqual(keys, want) {
		t.Fatalf("reflectSnapshotKeys = %v, want %v", sortedSet(keys), sortedSet(want))
	}

	// (a) Missed service: a discovery that dropped "beta" must be caught.
	if setsEqual(keys, map[string]struct{}{"alpha": {}}) {
		t.Fatalf("setsEqual failed to detect a missing key — a dropped service would go unnoticed")
	}

	// (b) Extra / wrong key: a discovery that returned a key for NotASvc, or the
	// wrong casing, must be caught.
	if setsEqual(keys, map[string]struct{}{"alpha": {}, "beta": {}, "notasvc": {}}) {
		t.Fatalf("setsEqual failed to detect an extra key")
	}

	if setsEqual(keys, map[string]struct{}{"Alpha": {}, "Beta": {}}) {
		t.Fatalf("setsEqual treated a casing difference as equal")
	}

	// (c) Collision: two fields whose names lowercase to the same key. Discover
	// silently collapses them into one map entry; reflectSnapshotKeys flags the
	// collision so the main test's collision assertion fires.
	type colliding struct {
		Route53 fakeSnapshottable
		ROUTE53 fakeSnapshottable
	}

	_, cc := reflectSnapshotKeys(&colliding{})
	if len(cc) == 0 {
		t.Fatalf("reflectSnapshotKeys did not report the route53/ROUTE53 collision")
	}

	// Two fields implement Snapshottable, but Discover keys by lowercased name and
	// so collapses them into one entry — the silent data loss the collision report
	// exists to surface.
	if got := snapshot.Discover(&colliding{}); len(got) != 1 {
		t.Fatalf("Discover(&colliding{}) has %d keys, want 1 (collision collapsed)", len(got))
	}
}

// TestSnapshotServicesStableKeys guards the stability half: repeated calls on
// fresh providers yield the identical key set, so a snapshot written by one run
// restores into the same services on the next.
func TestSnapshotServicesStableKeys(t *testing.T) {
	a := keysOf(cloudemu.NewAWS().SnapshotServices())
	b := keysOf(cloudemu.NewAWS().SnapshotServices())
	sort.Strings(a)
	sort.Strings(b)

	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("SnapshotServices keys not stable across calls: %v vs %v", a, b)
	}
}

// TestSnapshotServicesOCIEmpty documents that a provider with no Snapshottable
// service yet (OCI today) discovers an empty map rather than erroring — the
// mechanism auto-includes services as they implement the interface.
func TestSnapshotServicesOCIEmpty(t *testing.T) {
	if got := cloudemu.NewOCI().SnapshotServices(); len(got) != 0 {
		t.Fatalf("oci SnapshotServices = %v, want empty", got)
	}
}

// TestReadFileRejectsIncompatibleSchema is the load-compat guard: a v2 (or any
// non-current) snapshot — whose bespoke per-kind layout this build no longer
// reads — is rejected with a clear error, not silently mis-restored.
func TestReadFileRejectsIncompatibleSchema(t *testing.T) {
	dir := t.TempDir()

	for _, body := range []string{
		`{"schemaVersion":2,"providers":{"aws":{"buckets":[{"name":"b"}]}}}`,
		`{"schemaVersion":1}`,
		`{"schemaVersion":999}`,
	} {
		path := filepath.Join(dir, "snap.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := persist.ReadFile(path)
		if err == nil {
			t.Fatalf("ReadFile(%s) = nil error, want compat rejection", body)
		}

		if !strings.Contains(err.Error(), "not compatible with this build") {
			t.Fatalf("ReadFile error = %q, want a clear compatibility message", err)
		}
	}
}

// reflectSnapshotKeys independently reproduces the auto-discovery contract that
// snapshot.Discover implements, so the discovery test can assert against a set it
// did not obtain from Discover. It reflects over the exported, non-nil struct
// fields of p and, for each whose type implements snapshot.Snapshottable, records
// the lowercased field name. It uses reflect.Type.Implements (not a value type
// assertion, as Discover does) so the two are not the same code path. Any key
// produced by more than one field is reported in collisions rather than silently
// overwritten — the divergence a plain map would hide.
func reflectSnapshotKeys(p any) (keys map[string]struct{}, collisions []string) {
	keys = map[string]struct{}{}
	seen := map[string]int{}

	v := reflect.ValueOf(p)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return keys, collisions
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return keys, collisions
	}

	snapType := reflect.TypeOf((*snapshot.Snapshottable)(nil)).Elem()
	t := v.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		fv := v.Field(i)
		if fv.Kind() == reflect.Pointer && fv.IsNil() {
			continue
		}

		if !fv.Type().Implements(snapType) {
			continue
		}

		key := strings.ToLower(f.Name)
		seen[key]++
		keys[key] = struct{}{}
	}

	for k, n := range seen {
		if n > 1 {
			collisions = append(collisions, k)
		}
	}

	sort.Strings(collisions)

	return keys, collisions
}

// fakeSnapshottable is a minimal Snapshottable used only by the negative-path
// test to build fake provider structs with known field layouts.
type fakeSnapshottable struct{}

func (fakeSnapshottable) Snapshot(context.Context, bool) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (fakeSnapshottable) Restore(context.Context, json.RawMessage) error { return nil }

func keysOf(m persist.Services) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func asSet(keys []string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}

	return out
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func setsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}

	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}

	return true
}
