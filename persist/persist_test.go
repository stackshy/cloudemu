package persist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	"github.com/stackshy/cloudemu/v2/seed"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestExportRestoreRoundTrip is the core persistence guarantee: state exported
// from one provider, serialized to JSON, and restored into a fresh provider is
// intact — buckets/objects and tables/items survive a stop→start cycle.
func TestExportRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()
	tSrc := seed.Target{Storage: src.S3, Database: src.DynamoDB, Secrets: src.SecretsManager, Compute: src.EC2}

	if err := seed.Apply(ctx, seed.Fixtures{
		Buckets: []seed.Bucket{{
			Name:    "app-data",
			Objects: []seed.Object{{Key: "config.yaml", Body: "port: 8080", ContentType: "text/yaml"}},
		}},
		Tables: []seed.Table{{
			Name:         "users",
			PartitionKey: "id",
			Items:        []map[string]any{{"id": "u1", "name": "Ada"}},
		}},
		Secrets: []seed.Secret{{Name: "db-password", Value: "s3cr3t", Description: "prod db"}},
		Instances: []seed.Instance{{
			ImageID: "ami-123", InstanceType: "t3.micro", Count: 2, Name: "web",
		}},
	}, tSrc); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	ps, err := persist.Export(ctx, tSrc, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// Round-trip through JSON exactly as the on-disk snapshot would.
	raw, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored persist.ProviderState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	dst := cloudemu.NewAWS()
	tDst := seed.Target{Storage: dst.S3, Database: dst.DynamoDB, Secrets: dst.SecretsManager, Compute: dst.EC2}
	if err := persist.Restore(ctx, tDst, &restored); err != nil {
		t.Fatalf("restore: %v", err)
	}

	obj, err := dst.S3.GetObject(ctx, "app-data", "config.yaml")
	if err != nil {
		t.Fatalf("get restored object: %v", err)
	}
	if string(obj.Data) != "port: 8080" {
		t.Fatalf("restored object body = %q, want %q", obj.Data, "port: 8080")
	}
	if obj.Info.ContentType != "text/yaml" {
		t.Fatalf("restored content-type = %q, want text/yaml", obj.Info.ContentType)
	}

	res, err := dst.DynamoDB.Scan(ctx, dbdriver.ScanInput{Table: "users"})
	if err != nil {
		t.Fatalf("scan restored table: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0]["name"] != "Ada" {
		t.Fatalf("restored items = %v, want one item {id:u1,name:Ada}", res.Items)
	}

	sv, err := dst.SecretsManager.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("get restored secret: %v", err)
	}
	if string(sv.Value) != "s3cr3t" {
		t.Fatalf("restored secret value = %q, want s3cr3t", sv.Value)
	}

	insts, err := dst.EC2.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("describe restored instances: %v", err)
	}
	if len(insts) != 2 {
		t.Fatalf("restored instance count = %d, want 2", len(insts))
	}
	if insts[0].ImageID != "ami-123" || insts[0].Tags["Name"] != "web" {
		t.Fatalf("restored instance = %+v, want ami-123 / Name=web", insts[0])
	}
}

// TestExportAllRestoreAllMultiProvider covers the whole-emulator core used by
// both the persist-on-stop path and the snapshot admin endpoint: ExportAll
// captures every provider into one Snapshot, and RestoreAll puts each back into
// the matching target.
func TestExportAllRestoreAllMultiProvider(t *testing.T) {
	ctx := context.Background()

	aws, gcp := cloudemu.NewAWS(), cloudemu.NewGCP()
	src := map[string]seed.Target{
		"aws": {Storage: aws.S3, Database: aws.DynamoDB},
		"gcp": {Storage: gcp.GCS, Database: gcp.Firestore},
	}
	if err := seed.Apply(ctx, seed.Fixtures{Buckets: []seed.Bucket{{Name: "a", Objects: []seed.Object{{Key: "k", Body: "av"}}}}}, src["aws"]); err != nil {
		t.Fatalf("seed aws: %v", err)
	}
	if err := seed.Apply(ctx, seed.Fixtures{Buckets: []seed.Bucket{{Name: "g", Objects: []seed.Object{{Key: "k", Body: "gv"}}}}}, src["gcp"]); err != nil {
		t.Fatalf("seed gcp: %v", err)
	}

	snap, err := persist.ExportAll(ctx, src, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if snap.SchemaVersion != persist.SchemaVersion || len(snap.Providers) != 2 {
		t.Fatalf("ExportAll snapshot = %+v", snap)
	}

	raw, _ := json.Marshal(snap)
	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	aws2, gcp2 := cloudemu.NewAWS(), cloudemu.NewGCP()
	dst := map[string]seed.Target{
		"aws": {Storage: aws2.S3, Database: aws2.DynamoDB},
		"gcp": {Storage: gcp2.GCS, Database: gcp2.Firestore},
	}
	if err := persist.RestoreAll(ctx, &got, dst); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	ao, err := aws2.S3.GetObject(ctx, "a", "k")
	if err != nil || string(ao.Data) != "av" {
		t.Fatalf("aws restore = %v / %q", err, dataOf(ao))
	}
	go_, err := gcp2.GCS.GetObject(ctx, "g", "k")
	if err != nil || string(go_.Data) != "gv" {
		t.Fatalf("gcp restore = %v / %q", err, dataOf(go_))
	}
}

func dataOf(o *storagedriver.Object) []byte {
	if o == nil {
		return nil
	}

	return o.Data
}

// TestExportMetadataOnlyOmitsBodies verifies the default (metadata-only) export
// records object keys/metadata but drops the bytes, and that IncludeAssets keeps
// them — the flag that keeps the snapshot file KB-sized by default.
func TestExportMetadataOnlyOmitsBodies(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()
	tSrc := seed.Target{Storage: src.S3}
	if err := seed.Apply(ctx, seed.Fixtures{
		Buckets: []seed.Bucket{{Name: "b", Objects: []seed.Object{{Key: "k", Body: "secret-bytes"}}}},
	}, tSrc); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	meta, err := persist.Export(ctx, tSrc, persist.Options{IncludeAssets: false})
	if err != nil {
		t.Fatalf("export metadata-only: %v", err)
	}
	if len(meta.Buckets) != 1 || len(meta.Buckets[0].Objects) != 1 {
		t.Fatalf("metadata-only dropped bucket/object metadata: %+v", meta)
	}
	if len(meta.Buckets[0].Objects[0].Body) != 0 {
		t.Fatalf("metadata-only kept body: %q", meta.Buckets[0].Objects[0].Body)
	}

	full, err := persist.Export(ctx, tSrc, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("export with assets: %v", err)
	}
	if string(full.Buckets[0].Objects[0].Body) != "secret-bytes" {
		t.Fatalf("asset export dropped body: %q", full.Buckets[0].Objects[0].Body)
	}
}

// TestRestoreEmptyIsNoError confirms an empty/zero snapshot restores cleanly —
// a missing state file (first ever start) must not error.
func TestRestoreEmptyIsNoError(t *testing.T) {
	ctx := context.Background()
	dst := cloudemu.NewAWS()
	tDst := seed.Target{Storage: dst.S3, Database: dst.DynamoDB}
	if err := persist.Restore(ctx, tDst, &persist.ProviderState{}); err != nil {
		t.Fatalf("restore empty: %v", err)
	}
}

// TestExportRestorePreservesGSIs covers L2: a table's secondary indexes must
// survive snapshot/restore, or a Query against a GSI breaks after a restart.
func TestExportRestorePreservesGSIs(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()
	if err := src.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{
		Name:         "orders",
		PartitionKey: "id",
		GSIs:         []dbdriver.GSIConfig{{Name: "by-customer", PartitionKey: "customerId"}},
	}); err != nil {
		t.Fatalf("create table with GSI: %v", err)
	}

	ps, err := persist.Export(ctx, seed.Target{Database: src.DynamoDB}, persist.Options{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	dst := cloudemu.NewAWS()
	if err := persist.Restore(ctx, seed.Target{Database: dst.DynamoDB}, &ps); err != nil {
		t.Fatalf("restore: %v", err)
	}

	idxs, err := dst.DynamoDB.ListIndexes(ctx, "orders")
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	if len(idxs) != 1 || idxs[0].Name != "by-customer" || idxs[0].PartitionKey != "customerId" {
		t.Fatalf("GSI not restored: %+v", idxs)
	}
}

// TestSnapshotFileRoundTrip covers the on-disk layer: WriteFile then ReadFile
// preserves content, and an unknown schema version is rejected rather than
// silently mis-restored.
func TestSnapshotFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "snapshot.json")

	snap := persist.Snapshot{
		SchemaVersion: persist.SchemaVersion,
		Providers: map[string]persist.ProviderState{
			"aws": {Secrets: []persist.Secret{{Name: "k", Value: []byte("v")}}},
		},
	}
	if err := snap.WriteFile(path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := persist.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got.SchemaVersion != persist.SchemaVersion || len(got.Providers["aws"].Secrets) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// A snapshot from a future/unknown schema must be rejected, not mis-parsed.
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"schemaVersion":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := persist.ReadFile(bad); err == nil {
		t.Fatal("ReadFile(unknown schema) = nil error, want rejection")
	}
}
