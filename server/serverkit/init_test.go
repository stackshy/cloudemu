package serverkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/seed"
)

func TestApplyInitDirAppliesFixtures(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Files apply in lexical order; content is provider-agnostic seed fixtures.
	if err := os.WriteFile(filepath.Join(dir, "01-buckets.json"), []byte(`{"buckets":[{"name":"b"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "02-tables.json"), []byte(`{"tables":[{"name":"t","partitionKey":"id"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-json file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatal(err)
	}

	aws := cloudemu.NewAWS()
	targets := map[string]seed.Target{"aws": {Storage: aws.S3, Database: aws.DynamoDB}}
	if err := applyInitDir(ctx, dir, targets); err != nil {
		t.Fatalf("applyInitDir: %v", err)
	}

	buckets, err := aws.S3.ListBuckets(ctx)
	if err != nil || len(buckets) != 1 || buckets[0].Name != "b" {
		t.Fatalf("bucket not created from init dir: %v %v", buckets, err)
	}

	tables, err := aws.DynamoDB.ListTables(ctx)
	if err != nil || len(tables) != 1 || tables[0] != "t" {
		t.Fatalf("table not created from init dir: %v %v", tables, err)
	}
}

func TestApplyInitDirMissingIsNoOp(t *testing.T) {
	aws := cloudemu.NewAWS()
	targets := map[string]seed.Target{"aws": {Storage: aws.S3}}
	if err := applyInitDir(context.Background(), filepath.Join(t.TempDir(), "absent"), targets); err != nil {
		t.Fatalf("applyInitDir(missing) = %v, want nil", err)
	}
}

func TestApplyInitDirParseErrorFailsBoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{ not valid"), 0o600); err != nil {
		t.Fatal(err)
	}

	aws := cloudemu.NewAWS()
	targets := map[string]seed.Target{"aws": {Storage: aws.S3}}
	if err := applyInitDir(context.Background(), dir, targets); err == nil {
		t.Fatal("applyInitDir(bad json) = nil, want parse error")
	}
}

func TestApplyInitDirDuplicateWarnsNotFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// A fixture whose FIRST resource (bucket "dup") already exists, followed by a
	// NEW resource (table "fresh"). The collision must not truncate the rest of
	// the fixture — "fresh" must still be created.
	fixture := `{"buckets":[{"name":"dup"}],"tables":[{"name":"fresh","partitionKey":"id"}]}`
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	aws := cloudemu.NewAWS()
	targets := map[string]seed.Target{"aws": {Storage: aws.S3, Database: aws.DynamoDB}}
	// Pre-create the bucket so the init apply hits AlreadyExists on the first item.
	if err := aws.S3.CreateBucket(ctx, "dup"); err != nil {
		t.Fatal(err)
	}

	// The collision must warn-and-continue, not fail boot.
	if err := applyInitDir(ctx, dir, targets); err != nil {
		t.Fatalf("applyInitDir(duplicate) = %v, want nil (warn+continue)", err)
	}

	// The resource AFTER the collision must still have been created.
	tables, err := aws.DynamoDB.ListTables(ctx)
	if err != nil || len(tables) != 1 || tables[0] != "fresh" {
		t.Fatalf("post-collision table not created: %v %v", tables, err)
	}
}
