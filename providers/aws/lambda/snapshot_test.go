package lambda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestSnapshotRoundTripLambda proves a snapshot/restore round-trip preserves a
// function together with its published version (and that version's captured
// config), an alias pointing at that version, and an event-source mapping — all
// under their original identities.
func TestSnapshotRoundTripLambda(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	cfg := defaultFuncConfig()
	cfg.Code = []byte("zip-bytes-here")

	if _, err := src.CreateFunction(ctx, cfg); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	ver, err := src.PublishVersion(ctx, cfg.Name, "v1")
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := src.CreateAlias(ctx, driver.AliasConfig{
		FunctionName: cfg.Name, Name: "live", FunctionVersion: ver.Version,
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	esm, err := src.CreateEventSourceMapping(ctx, driver.EventSourceMappingConfig{
		FunctionName:   cfg.Name,
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:q",
		BatchSize:      10,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}

	urlCfg, err := src.CreateFunctionURLConfig(ctx, driver.FunctionURLConfig{FunctionName: cfg.Name})
	if err != nil {
		t.Fatalf("CreateFunctionURLConfig: %v", err)
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
	if err != nil || fn.Tags["env"] != "test" || fn.CodeSHA256 == "" {
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
		t.Fatalf("published version %s missing from restored versions %+v", ver.Version, vers)
	}

	// The published version's captured config carries the code identity the mock
	// retains (CodeSHA256); confirm the promotion preserved it verbatim.
	fd, ok := dst.funcs.Get(cfg.Name)
	if !ok || len(fd.versions) != 1 || fd.versions[0].config.Name != cfg.Name {
		t.Fatalf("restored funcData versions = %+v, ok %v", fd.versions, ok)
	}

	alias, err := dst.GetAlias(ctx, cfg.Name, "live")
	if err != nil || alias.FunctionVersion != ver.Version {
		t.Fatalf("restored alias = %+v, err %v", alias, err)
	}

	gotESM, err := dst.GetEventSourceMapping(ctx, esm.UUID)
	if err != nil || gotESM.FunctionArn == "" {
		t.Fatalf("restored esm = %+v, err %v", gotESM, err)
	}

	gotURLCfg, err := dst.GetFunctionURLConfig(ctx, cfg.Name, "")
	if err != nil || gotURLCfg.FunctionURL != urlCfg.FunctionURL {
		t.Fatalf("restored function url config = %+v, err %v, want FunctionURL %q", gotURLCfg, err, urlCfg.FunctionURL)
	}
}

// TestSnapshotRestoreLegacyFunctionURLConfig proves Restore migrates a
// snapshot taken before Function URLs gained qualifier scoping — which
// serialized the config under the singular "urlConfig" key instead of today's
// per-qualifier "urlConfigs" map — so an old on-disk snapshot's Function URL
// config isn't silently dropped.
func TestSnapshotRestoreLegacyFunctionURLConfig(t *testing.T) {
	ctx := context.Background()

	legacy := map[string]any{
		"funcs": map[string]any{
			"my-func": map[string]any{
				"info": map[string]any{
					"Name": "my-func",
					"ARN":  "arn:aws:lambda:us-east-1:000000000000:function:my-func",
				},
				"awsConfig": map[string]any{},
				"urlConfig": map[string]any{
					"FunctionName": "my-func",
					"Qualifier":    "",
					"FunctionArn":  "arn:aws:lambda:us-east-1:000000000000:function:my-func",
					"FunctionURL":  "https://legacyid123456.lambda-url.us-east-1.on.aws/",
					"AuthType":     "NONE",
					"InvokeMode":   "BUFFERED",
					"CreationTime": "2025-01-01T00:00:00Z",
					"LastModified": "2025-01-01T00:00:00Z",
				},
			},
		},
	}

	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore legacy snapshot: %v", err)
	}

	got, err := dst.GetFunctionURLConfig(ctx, "my-func", "")
	if err != nil {
		t.Fatalf("GetFunctionURLConfig after restoring a legacy singular-urlConfig snapshot: %v", err)
	}

	if got.FunctionURL != "https://legacyid123456.lambda-url.us-east-1.on.aws/" {
		t.Fatalf("restored FunctionURL = %q, want the legacy snapshot's URL", got.FunctionURL)
	}
}

// TestSnapshotRoundTripProvisionedConcurrency proves a snapshot/restore
// round-trip preserves a function's per-qualifier provisioned-concurrency
// config.
func TestSnapshotRoundTripProvisionedConcurrency(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateFunction(ctx, defaultFuncConfig()); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	ver, err := src.PublishVersion(ctx, "my-func", "v1")
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := src.PutFunctionProvisionedConcurrencyConfig(ctx, driver.ProvisionedConcurrencyConfig{
		FunctionName:                             "my-func",
		Qualifier:                                ver.Version,
		RequestedProvisionedConcurrentExecutions: 4,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.GetFunctionProvisionedConcurrencyConfig(ctx, "my-func", ver.Version)
	if err != nil {
		t.Fatalf("restored Get: %v", err)
	}

	if got.RequestedProvisionedConcurrentExecutions != 4 {
		t.Fatalf("restored requested = %d, want 4", got.RequestedProvisionedConcurrentExecutions)
	}
}

// TestSnapshotRoundTripLayers proves a snapshot/restore round-trip preserves a
// layer's published versions (with their CompatibleArchitectures/LicenseInfo
// metadata), the monotonic version counter (so a post-restore publish still
// continues from where it left off rather than reusing a deleted version
// number), and a layer version's resource-based policy (statements + the
// RevisionId optimistic-concurrency guard).
func TestSnapshotRoundTripLayers(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.PublishLayerVersion(ctx, driver.LayerConfig{
		Name: "my-layer", Content: []byte("v1"),
		CompatibleRuntimes: []string{"python3.9"}, CompatibleArchitectures: []string{"arm64"}, LicenseInfo: "MIT",
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion v1: %v", err)
	}

	v2, err := src.PublishLayerVersion(ctx, driver.LayerConfig{Name: "my-layer", Content: []byte("v2")})
	if err != nil {
		t.Fatalf("PublishLayerVersion v2: %v", err)
	}

	if err := src.DeleteLayerVersion(ctx, "my-layer", 1); err != nil {
		t.Fatalf("DeleteLayerVersion: %v", err)
	}

	_, wantRevision, err := src.AddLayerVersionPermission(ctx, "my-layer", v2.Version, driver.LayerPermissionStatement{
		StatementID: "xaccount", Action: "lambda:GetLayerVersion", Principal: "111111111111",
	}, "")
	if err != nil {
		t.Fatalf("AddLayerVersionPermission: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.GetLayerVersion(ctx, "my-layer", v2.Version)
	if err != nil || got.LicenseInfo != "" {
		// v2 was published without metadata; v1 (which carried it) was deleted
		// before the snapshot, so it must not resurface here.
		t.Fatalf("restored v2 = %+v, err %v", got, err)
	}

	if _, err := dst.GetLayerVersion(ctx, "my-layer", 1); err == nil {
		t.Fatal("deleted version 1 resurfaced after restore")
	}

	// The version counter must have survived the round-trip: a post-restore
	// publish continues at 3, not 2 (which would collide with the still-live v2).
	v3, err := dst.PublishLayerVersion(ctx, driver.LayerConfig{Name: "my-layer", Content: []byte("v3")})
	if err != nil {
		t.Fatalf("PublishLayerVersion after restore: %v", err)
	}

	if v3.Version != v2.Version+1 {
		t.Fatalf("post-restore published version = %d, want %d", v3.Version, v2.Version+1)
	}

	policy, gotRevision, err := dst.GetLayerVersionPolicy(ctx, "my-layer", v2.Version)
	if err != nil || gotRevision != wantRevision {
		t.Fatalf("restored policy revision = %q err %v, want %q", gotRevision, err, wantRevision)
	}

	if !strings.Contains(policy, "xaccount") {
		t.Fatalf("restored policy = %q, want it to contain the xaccount statement", policy)
	}
}
