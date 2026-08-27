package functions

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestSnapshotRoundTripFunctions proves a snapshot/restore round-trip preserves
// a function together with its published version (and captured config) under
// their original identities.
func TestSnapshotRoundTripFunctions(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	cfg := driver.FunctionConfig{
		Name: "myFunc", Runtime: "dotnet6", Handler: "MyApp::Handler",
		Memory: 256, Timeout: 30, Tags: map[string]string{"team": "backend"},
		Code: []byte("zip-bytes"),
	}
	if _, err := src.CreateFunction(ctx, cfg); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	ver, err := src.PublishVersion(ctx, cfg.Name, "v1")
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	fn, err := dst.GetFunction(ctx, cfg.Name)
	if err != nil || fn.Tags["team"] != "backend" || fn.Runtime != "dotnet6" {
		t.Fatalf("restored function = %+v, err %v", fn, err)
	}

	vers, err := dst.ListVersions(ctx, cfg.Name)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	var found bool
	for _, v := range vers {
		if v.Version == ver.Version {
			found = true
		}
	}

	if !found {
		t.Fatalf("published version %s missing from %+v", ver.Version, vers)
	}

	// The published version's captured config survived the promotion.
	fd, ok := dst.funcs.Get(cfg.Name)
	if !ok || len(fd.versions) != 1 || fd.versions[0].config.Name != cfg.Name {
		t.Fatalf("restored funcData versions = %+v, ok %v", fd.versions, ok)
	}
}
