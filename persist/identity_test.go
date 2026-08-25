package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	"github.com/stackshy/cloudemu/v2/seed"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestIdentityPreservedAcrossRestore is the headline guarantee of the per-driver
// snapshot: after a full Export→JSON→Restore into a FRESH provider, resource
// identifiers and id-string cross-references survive unchanged. In particular an
// EC2 instance keeps the SAME instance id and its security-group reference — the
// thing the old driver-replay compute path could not do (it minted a fresh id).
func TestIdentityPreservedAcrossRestore(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()

	// S3 bucket + object (with bytes).
	if err := src.S3.CreateBucket(ctx, "app-data"); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := src.S3.PutObject(ctx, "app-data", "config.yaml", []byte("port: 8080"), "text/yaml", nil); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// DynamoDB table + item.
	if err := src.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{Name: "users", PartitionKey: "id"}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := src.DynamoDB.PutItem(ctx, "users", map[string]any{"id": "u1", "name": "Ada"}); err != nil {
		t.Fatalf("put item: %v", err)
	}

	// Secret.
	if _, err := src.SecretsManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "db-password"}, []byte("s3cr3t")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// EC2 instance launched with a security group.
	const sgID = "sg-0abc123"

	launched, err := src.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-123", InstanceType: "t3.micro", SecurityGroups: []string{sgID},
	}, 1)
	if err != nil {
		t.Fatalf("run instances: %v", err)
	}
	if len(launched) != 1 {
		t.Fatalf("launched %d instances, want 1", len(launched))
	}

	wantInstanceID := launched[0].ID

	// Export the whole emulator to JSON bytes, exactly as the on-disk snapshot.
	tSrc := seed.Target{Storage: src.S3, Database: src.DynamoDB, Secrets: src.SecretsManager, Compute: src.EC2}

	snap, err := persist.ExportAll(ctx, map[string]seed.Target{"aws": tSrc}, persist.Options{IncludeAssets: true})
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
	dst := cloudemu.NewAWS()
	tDst := seed.Target{Storage: dst.S3, Database: dst.DynamoDB, Secrets: dst.SecretsManager, Compute: dst.EC2}
	if err := persist.RestoreAll(ctx, &got, map[string]seed.Target{"aws": tDst}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// EC2 instance keeps its identity and its SG cross-reference.
	insts, err := dst.EC2.DescribeInstances(ctx, nil, nil)
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
	// machine was re-registered, so a Stop succeeds (it would fail "not found" if
	// only the record — not the FSM state — had been restored).
	if err := dst.EC2.StopInstances(ctx, []string{wantInstanceID}); err != nil {
		t.Fatalf("stop restored instance: %v", err)
	}

	// S3 object bytes survive.
	obj, err := dst.S3.GetObject(ctx, "app-data", "config.yaml")
	if err != nil {
		t.Fatalf("get restored object: %v", err)
	}
	if string(obj.Data) != "port: 8080" {
		t.Fatalf("restored object body = %q, want %q", obj.Data, "port: 8080")
	}

	// DynamoDB item survives.
	item, err := dst.DynamoDB.GetItem(ctx, "users", map[string]any{"id": "u1"})
	if err != nil {
		t.Fatalf("get restored item: %v", err)
	}
	if item == nil || item["name"] != "Ada" {
		t.Fatalf("restored item = %v, want {id:u1,name:Ada}", item)
	}

	// Secret value survives.
	sv, err := dst.SecretsManager.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("get restored secret: %v", err)
	}
	if string(sv.Value) != "s3cr3t" {
		t.Fatalf("restored secret value = %q, want s3cr3t", sv.Value)
	}
}
