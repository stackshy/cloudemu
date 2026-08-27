package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestIdentityPreservedAcrossRestoreGCP is the GCP analogue of the AWS identity
// guarantee: after a full Export→JSON→Restore into a FRESH GCP provider,
// resource identifiers and id-string cross-references survive unchanged. In
// particular a GCE instance keeps the SAME instance id and its security-group
// reference — the thing the old driver-replay compute path could not do.
func TestIdentityPreservedAcrossRestoreGCP(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewGCP()

	// GCS bucket + object (with bytes).
	if err := src.GCS.CreateBucket(ctx, "app-data"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := src.GCS.PutObject(ctx, "app-data", "config.yaml", []byte("port: 8080"), "text/yaml", nil); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// Firestore collection + document.
	if err := src.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: "users", PartitionKey: "id"}); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if err := src.Firestore.PutItem(ctx, "users", map[string]any{"id": "u1", "name": "Ada"}); err != nil {
		t.Fatalf("put document: %v", err)
	}

	// Secret Manager secret.
	if _, err := src.SecretManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "db-password"}, []byte("s3cr3t")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// GCE instance launched with a security group.
	const sgID = "fw-0abc123"

	launched, err := src.GCE.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "debian-12", InstanceType: "e2-medium", SecurityGroups: []string{sgID},
	}, 1)
	if err != nil {
		t.Fatalf("run instances: %v", err)
	}
	if len(launched) != 1 {
		t.Fatalf("launched %d instances, want 1", len(launched))
	}

	wantInstanceID := launched[0].ID

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"gcp": src.SnapshotServices()}, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	// Restore into a completely fresh provider.
	dst := cloudemu.NewGCP()
	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{"gcp": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// GCE instance keeps its identity and its SG cross-reference.
	insts, err := dst.GCE.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("describe restored instances: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("restored %d instances, want 1", len(insts))
	}
	if insts[0].ID != wantInstanceID {
		t.Fatalf("restored instance id = %q, want SAME id %q", insts[0].ID, wantInstanceID)
	}
	if len(insts[0].SecurityGroups) != 1 || insts[0].SecurityGroups[0] != sgID {
		t.Fatalf("restored SG reference = %v, want [%s]", insts[0].SecurityGroups, sgID)
	}

	// The restored instance is still a live, transitionable resource: the state
	// machine was re-registered, so a Stop succeeds (it would fail if only the
	// record — not the FSM state — had been restored).
	if err := dst.GCE.StopInstances(ctx, []string{wantInstanceID}); err != nil {
		t.Fatalf("stop restored instance: %v", err)
	}

	// GCS object bytes survive.
	obj, err := dst.GCS.GetObject(ctx, "app-data", "config.yaml")
	if err != nil {
		t.Fatalf("get restored object: %v", err)
	}
	if string(obj.Data) != "port: 8080" {
		t.Fatalf("restored object body = %q, want %q", obj.Data, "port: 8080")
	}

	// Firestore document survives.
	item, err := dst.Firestore.GetItem(ctx, "users", map[string]any{"id": "u1"})
	if err != nil {
		t.Fatalf("get restored document: %v", err)
	}
	if item == nil || item["name"] != "Ada" {
		t.Fatalf("restored document = %v, want {id:u1,name:Ada}", item)
	}

	// Secret value survives.
	sv, err := dst.SecretManager.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("get restored secret: %v", err)
	}
	if string(sv.Value) != "s3cr3t" {
		t.Fatalf("restored secret value = %q, want s3cr3t", sv.Value)
	}
}
