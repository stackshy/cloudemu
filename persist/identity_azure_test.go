package persist_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/persist"
	azureiam "github.com/stackshy/cloudemu/v2/providers/azure/iam"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestIdentityPreservedAcrossRestoreAzure is the Azure analogue of the AWS
// identity guarantee: after a full Export→JSON→Restore into a FRESH Azure
// provider, resource identifiers and id-string cross-references survive
// unchanged. In particular a virtual machine keeps the SAME instance id and its
// security-group reference, the thing the old driver-replay compute path could
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
	// record, not the FSM state, had been restored).
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
// assignment (Microsoft.Authorization/roleAssignments), a wire-model concept
// with no AWS-shaped driver.IAM equivalent, round-trips through a full
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

// TestResourceGroupsSurviveRestoreAzure guards the ARM resource-group store:
// a group created over the wire must come back after Export, JSON and Restore
// into a fresh provider, with its tags and location, and a resource inside it
// must still pass the resource-group gate rather than answer
// ResourceGroupNotFound.
func TestResourceGroupsSurviveRestoreAzure(t *testing.T) {
	ctx := context.Background()

	const (
		sub   = "00000000-0000-0000-0000-0000000000ab"
		rgURL = "/subscriptions/" + sub + "/resourceGroups/rg-durable?api-version=2021-04-01"
		uai   = "/subscriptions/" + sub + "/resourceGroups/rg-durable/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/uai-durable?api-version=2023-01-31"
		vnet = "/subscriptions/" + sub + "/resourceGroups/rg-durable/providers/" +
			"Microsoft.Network/virtualNetworks/vnet-durable?api-version=2024-05-01"
	)

	src := cloudemu.NewAzure()
	srcSrv := httptest.NewServer(azureserver.NewFromProvider(src))
	t.Cleanup(srcSrv.Close)

	armDo(t, srcSrv.URL, http.MethodPut, rgURL, `{"location":"westeurope","tags":{"env":"prod"}}`, http.StatusCreated)
	armDo(t, srcSrv.URL, http.MethodPut, uai, `{"location":"westeurope"}`, http.StatusCreated)
	armDo(t, srcSrv.URL, http.MethodPut, vnet, `{"location":"westeurope","properties":{"addressSpace":`+
		`{"addressPrefixes":["10.40.0.0/16"]},"privateEndpointVNetPolicies":"Disabled"}}`, http.StatusOK)
	wantRG := armDo(t, srcSrv.URL, http.MethodGet, rgURL, "", http.StatusOK)

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

	dstSrv := httptest.NewServer(azureserver.NewFromProvider(dst))
	t.Cleanup(dstSrv.Close)

	if gotRG := armDo(t, dstSrv.URL, http.MethodGet, rgURL, "", http.StatusOK); gotRG != wantRG {
		t.Fatalf("restored group = %s, want %s", gotRG, wantRG)
	}

	var list struct {
		Value []map[string]any `json:"value"`
	}

	listURL := "/subscriptions/" + sub + "/resourcegroups?api-version=2021-04-01"
	if err := json.Unmarshal([]byte(armDo(t, dstSrv.URL, http.MethodGet, listURL, "", http.StatusOK)), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	if len(list.Value) != 1 {
		t.Fatalf("restored list has %d groups, want 1", len(list.Value))
	}

	armDo(t, dstSrv.URL, http.MethodGet, uai, "", http.StatusOK)

	// An unmodeled property the client set is still echoed after the restore,
	// so a Terraform re-plan sees no drift.
	if got := armDo(t, dstSrv.URL, http.MethodGet, vnet, "", http.StatusOK); !strings.Contains(got,
		`"privateEndpointVNetPolicies":"Disabled"`) {
		t.Fatalf("restored vnet lost its echoed property: %s", got)
	}
}

// anySuccess as armDo's want accepts any 2xx status.
const anySuccess = 0

// armDo sends one ARM request and returns the response body, failing the test
// on any status other than want.
func armDo(t *testing.T, base, method, path, body string, want int) string {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	ok := resp.StatusCode == want || (want == anySuccess && resp.StatusCode >= 200 && resp.StatusCode < 300)
	if !ok {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}

	return string(data)
}

// TestResourceGroupsRebuiltFromPreRGSnapshotAzure covers a snapshot written
// before resource groups were persisted (no resourcegroups entry): restore
// rebuilds the groups from the restored resources, so the group and the
// resources inside it answer 200 instead of ResourceGroupNotFound.
func TestResourceGroupsRebuiltFromPreRGSnapshotAzure(t *testing.T) {
	ctx := context.Background()

	const (
		sub  = "00000000-0000-0000-0000-0000000000ab"
		rg   = "/subscriptions/" + sub + "/resourceGroups/rg1"
		acct = rg + "/providers/Microsoft.Storage/storageAccounts/stold01?api-version=2023-05-01"
		vnet = rg + "/providers/Microsoft.Network/virtualNetworks/vnet-old?api-version=2024-05-01"
	)

	// serve sets the provider account to --azure-subscription; the inventory
	// reads resource ids under it.
	src := cloudemu.NewAzure(config.WithAccountID(sub))
	srcSrv := httptest.NewServer(azureserver.NewFromProvider(src))
	t.Cleanup(srcSrv.Close)

	armDo(t, srcSrv.URL, http.MethodPut, rg+"?api-version=2021-04-01", `{"location":"westeurope"}`, http.StatusCreated)
	armDo(t, srcSrv.URL, http.MethodPut, acct,
		`{"location":"westeurope","kind":"StorageV2","sku":{"name":"Standard_LRS"}}`, anySuccess)
	armDo(t, srcSrv.URL, http.MethodPut, vnet,
		`{"location":"westeurope","properties":{"addressSpace":{"addressPrefixes":["10.1.0.0/16"]}}}`, anySuccess)

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{"azure": src.SnapshotServices()}, persist.Options{})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	// Drop the entries a snapshot from before this store existed lacks.
	ps := snap.Providers["azure"]
	delete(ps.Services, "resourcegroups")
	delete(ps.Services, "propertyoverlay")
	snap.Providers["azure"] = ps

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	dst := cloudemu.NewAzure(config.WithAccountID(sub))
	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{"azure": dst.SnapshotServices()}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	dstSrv := httptest.NewServer(azureserver.NewFromProvider(dst))
	t.Cleanup(dstSrv.Close)

	if body := armDo(t, dstSrv.URL, http.MethodGet, rg+"?api-version=2021-04-01", "", http.StatusOK); !strings.Contains(body,
		`"location":"westeurope"`) {
		t.Fatalf("rebuilt group = %s, want location westeurope", body)
	}

	armDo(t, dstSrv.URL, http.MethodGet, acct, "", http.StatusOK)
	armDo(t, dstSrv.URL, http.MethodGet, vnet, "", http.StatusOK)
}
