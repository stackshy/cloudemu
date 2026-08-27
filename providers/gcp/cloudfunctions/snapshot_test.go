package cloudfunctions

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestSnapshotRoundTripCloudFunctions proves a snapshot/restore round-trip
// preserves a function, its published version (with CodeSHA), an alias, and a
// layer version under their original identities.
func TestSnapshotRoundTripCloudFunctions(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateFunction(ctx, driver.FunctionConfig{
		Name: "fn1", Runtime: "go121", Handler: "Handle", Memory: 256, Timeout: 60,
		Code: []byte("deployment-package-bytes"), Framework: "http",
	}); err != nil {
		t.Fatalf("create function: %v", err)
	}

	ver, err := src.PublishVersion(ctx, "fn1", "v1")
	if err != nil {
		t.Fatalf("publish version: %v", err)
	}

	if _, err := src.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: "fn1", Name: "prod", FunctionVersion: ver.Version,
	}); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	if _, err := src.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "shared", Content: []byte("layer-bytes"), CompatibleRuntimes: []string{"go121"},
	}); err != nil {
		t.Fatalf("publish layer: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	fn, err := dst.GetFunction(ctx, "fn1")
	if err != nil || fn.Runtime != "go121" || fn.Handler != "Handle" {
		t.Fatalf("restored function = %+v, err %v", fn, err)
	}

	vers, err := dst.ListVersions(ctx, "fn1")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}

	var found bool

	for _, v := range vers {
		if v.Version == "1" && v.CodeSHA256 == ver.CodeSHA256 {
			found = true
		}
	}

	if !found {
		t.Fatalf("published version 1 with sha %q not found in %+v", ver.CodeSHA256, vers)
	}

	if a, err := dst.GetAlias(ctx, "fn1", "prod"); err != nil || a.FunctionVersion != "1" {
		t.Fatalf("restored alias = %+v, err %v", a, err)
	}

	if lv, err := dst.GetLayerVersion(ctx, "shared", 1); err != nil || lv.ContentSize != int64(len("layer-bytes")) {
		t.Fatalf("restored layer version = %+v, err %v", lv, err)
	}
}
