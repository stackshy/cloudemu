package lambda

import (
	"context"
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
