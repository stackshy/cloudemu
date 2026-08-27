package persist_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
)

// TestSnapshotServicesDiscovery pins that each provider factory's
// SnapshotServices() enumerates exactly the services that implement
// snapshot.Snapshottable today, keyed by a stable lowercased field-name key.
// This is the auto-discovery contract persist relies on: adding a Snapshottable
// service later grows this map with no persist change, and the keys must stay
// stable so a snapshot restores into the right service across runs.
func TestSnapshotServicesDiscovery(t *testing.T) {
	tests := []struct {
		provider string
		services map[string]struct{}
		want     []string
	}{
		{"aws", asSet(keysOf(cloudemu.NewAWS().SnapshotServices())), []string{"dynamodb", "ec2", "s3", "secretsmanager", "vpc"}},
		{"azure", asSet(keysOf(cloudemu.NewAzure().SnapshotServices())), []string{"blobstorage", "cosmosdb", "keyvault", "virtualmachines", "vnet"}},
		{"gcp", asSet(keysOf(cloudemu.NewGCP().SnapshotServices())), []string{"firestore", "gce", "gcs", "secretmanager", "vpc"}},
	}

	for _, tc := range tests {
		got := sortedSet(tc.services)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s SnapshotServices keys = %v, want %v", tc.provider, got, tc.want)
		}

		for _, k := range got {
			if k != strings.ToLower(k) {
				t.Errorf("%s key %q is not lowercased (keys must be stable, case-normalized)", tc.provider, k)
			}
		}
	}

	// Values must be non-nil (a nil Snapshottable would panic on Snapshot) — the
	// discovery skips nil pointer fields.
	for name, s := range cloudemu.NewAWS().SnapshotServices() {
		if s == nil {
			t.Errorf("aws service %q discovered as nil", name)
		}
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
