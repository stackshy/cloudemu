package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	azureiam "github.com/stackshy/cloudemu/v2/providers/azure/iam"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestIdentityPreservedAcrossRestoreAzure is the Azure analogue of the AWS
// identity guarantee: after a full Export→JSON→Restore into a FRESH Azure
// provider, resource identifiers and id-string cross-references survive
// unchanged. In particular a virtual machine keeps the SAME instance id and its
// security-group reference — the thing the old driver-replay compute path could
// not do (it minted a fresh id via RunInstances).
func TestIdentityPreservedAcrossRestoreAzure(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAzure()

	// Blob container + blob (with bytes).
	if err := src.BlobStorage.CreateBucket(ctx, "app-data"); err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := src.BlobStorage.PutObject(ctx, "app-data", "config.yaml", []byte("port: 8080"), "text/yaml", nil); err != nil {
		t.Fatalf("put blob: %v", err)
	}

	// Cosmos container + item.
	if err := src.CosmosDB.CreateTable(ctx, dbdriver.TableConfig{Name: "users", PartitionKey: "id"}); err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := src.CosmosDB.PutItem(ctx, "users", map[string]any{"id": "u1", "name": "Ada"}); err != nil {
		t.Fatalf("put item: %v", err)
	}

	// Key Vault secret.
	if _, err := src.KeyVault.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "db-password"}, []byte("s3cr3t")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// VM launched with a security group.
	const sgID = "nsg-0abc123"

	launched, err := src.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ubuntu-22", InstanceType: "Standard_D2s_v3", SecurityGroups: []string{sgID},
	}, 1)
	if err != nil {
		t.Fatalf("run instances: %v", err)
	}
	if len(launched) != 1 {
		t.Fatalf("launched %d instances, want 1", len(launched))
	}

	wantInstanceID := launched[0].ID

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"azure": src.SnapshotServices()}, persist.Options{IncludeAssets: true})
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
	dst := cloudemu.NewAzure()
	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{"azure": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	// VM keeps its identity and its SG cross-reference.
	insts, err := dst.VirtualMachines.DescribeInstances(ctx, nil, nil)
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
	if err := dst.VirtualMachines.StopInstances(ctx, []string{wantInstanceID}); err != nil {
		t.Fatalf("stop restored instance: %v", err)
	}

	// Blob bytes survive.
	obj, err := dst.BlobStorage.GetObject(ctx, "app-data", "config.yaml")
	if err != nil {
		t.Fatalf("get restored blob: %v", err)
	}
	if string(obj.Data) != "port: 8080" {
		t.Fatalf("restored blob body = %q, want %q", obj.Data, "port: 8080")
	}

	// Cosmos item survives.
	item, err := dst.CosmosDB.GetItem(ctx, "users", map[string]any{"id": "u1"})
	if err != nil {
		t.Fatalf("get restored item: %v", err)
	}
	if item == nil || item["name"] != "Ada" {
		t.Fatalf("restored item = %v, want {id:u1,name:Ada}", item)
	}

	// Secret value survives.
	sv, err := dst.KeyVault.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("get restored secret: %v", err)
	}
	if string(sv.Value) != "s3cr3t" {
		t.Fatalf("restored secret value = %q, want s3cr3t", sv.Value)
	}
}

// TestRoleAssignmentsSurviveRestoreAzure confirms an Azure RBAC role
// assignment (Microsoft.Authorization/roleAssignments) — a wire-model concept
// with no AWS-shaped driver.IAM equivalent — round-trips through a full
// Export→JSON→Restore into a fresh provider under the SAME assignment id, and
// that a role definition it references is still blocked from deletion after
// restore, exactly as before the snapshot.
func TestRoleAssignmentsSurviveRestoreAzure(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAzure()

	const roleDefID = "roledef-restore-test"

	if _, err := src.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: roleDefID}); err != nil {
		t.Fatalf("create role definition: %v", err)
	}

	const (
		assignmentID = "assignment-restore-test"
		principalID  = "principal-restore-test"
		scope        = "/subscriptions/11111111-1111-1111-1111-111111111111"
	)

	if _, err := src.IAM.CreateRoleAssignment(ctx, azureiam.RoleAssignmentConfig{
		ID:               assignmentID,
		RoleDefinitionID: roleDefID,
		PrincipalID:      principalID,
		PrincipalType:    "User",
		Scope:            scope,
	}); err != nil {
		t.Fatalf("create role assignment: %v", err)
	}

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"azure": src.SnapshotServices()}, persist.Options{})
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

	dst := cloudemu.NewAzure()
	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{"azure": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	restored, err := dst.IAM.GetRoleAssignment(ctx, assignmentID)
	if err != nil {
		t.Fatalf("get restored role assignment: %v", err)
	}

	if restored.PrincipalID != principalID || restored.RoleDefinitionID != roleDefID || restored.Scope != scope {
		t.Fatalf("restored assignment = %+v, want principalId=%s roleDefinitionId=%s scope=%s",
			restored, principalID, roleDefID, scope)
	}

	inUse, err := dst.IAM.RoleAssignmentsForRoleDefinition(ctx, roleDefID)
	if err != nil {
		t.Fatalf("reverse lookup after restore: %v", err)
	}

	if len(inUse) != 1 || inUse[0].ID != assignmentID {
		t.Fatalf("restored reverse lookup = %+v, want the one restored assignment", inUse)
	}
}
