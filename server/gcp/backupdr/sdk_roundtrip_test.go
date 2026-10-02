package backupdr_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	backupdr "google.golang.org/api/backupdr/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpprovider "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

const (
	sdkProject  = "mock-project"
	sdkLocation = "us-central1"
	retention   = "86400s"
	maxPolls    = 10
)

type sdkEnv struct {
	svc    *backupdr.Service
	cloud  *gcpprovider.Provider
	clock  *config.FakeClock
	parent string
}

func newSDKEnv(t *testing.T) *sdkEnv {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC))
	cloud := cloudemu.NewGCP(config.WithClock(clk))

	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	svc, err := backupdr.NewService(context.Background(),
		option.WithEndpoint(ts.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("backupdr.NewService: %v", err)
	}

	return &sdkEnv{
		svc: svc, cloud: cloud, clock: clk,
		parent: "projects/" + sdkProject + "/locations/" + sdkLocation,
	}
}

// wait polls an operation through Projects.Locations.Operations.Get (the shared
// LRO poller in the assembled server) until it reports done.
func (e *sdkEnv) wait(t *testing.T, op *backupdr.Operation) *backupdr.Operation {
	t.Helper()

	for range maxPolls {
		polled, err := e.svc.Projects.Locations.Operations.Get(op.Name).Do()
		if err != nil {
			t.Fatalf("Operations.Get(%s): %v", op.Name, err)
		}

		if polled.Done {
			if polled.Error != nil {
				t.Fatalf("operation %s failed: %+v", op.Name, polled.Error)
			}

			return polled
		}
	}

	t.Fatalf("operation %s never completed", op.Name)

	return nil
}

func (e *sdkEnv) create(t *testing.T, id string, v *backupdr.BackupVault) *backupdr.Operation {
	t.Helper()

	op, err := e.svc.Projects.Locations.BackupVaults.Create(e.parent, v).BackupVaultId(id).Do()
	if err != nil {
		t.Fatalf("BackupVaults.Create(%s): %v", id, err)
	}

	return e.wait(t, op)
}

func (e *sdkEnv) get(t *testing.T, name string) *backupdr.BackupVault {
	t.Helper()

	v, err := e.svc.Projects.Locations.BackupVaults.Get(name).Do()
	if err != nil {
		t.Fatalf("BackupVaults.Get(%s): %v", name, err)
	}

	return v
}

// wantCode asserts err is a *googleapi.Error carrying the HTTP status code.
func wantCode(t *testing.T, what string, err error, code int) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != code {
		t.Fatalf("%s: err = %v, want HTTP %d", what, err, code)
	}
}

// TestSDKBackupVaultLifecycle drives create (LRO + poll), get, list, patch and
// delete through the real google.golang.org/api/backupdr/v1 client.
func TestSDKBackupVaultLifecycle(t *testing.T) {
	e := newSDKEnv(t)
	name := e.parent + "/backupVaults/vault-a"

	done := e.create(t, "vault-a", &backupdr.BackupVault{
		Description:                            "primary vault",
		Labels:                                 map[string]string{"env": "dev"},
		BackupMinimumEnforcedRetentionDuration: retention,
	})

	var fromOp backupdr.BackupVault
	if err := json.Unmarshal(done.Response, &fromOp); err != nil || fromOp.Name != name {
		t.Fatalf("operation response = %s (err %v), want vault %s", done.Response, err, name)
	}

	got := e.get(t, name)
	assertCreated(t, got, name)

	// Patch only the description; labels stay, etag and updateTime rotate.
	e.clock.Advance(time.Minute)

	patchOp, err := e.svc.Projects.Locations.BackupVaults.Patch(name, &backupdr.BackupVault{
		Description: "renamed",
		Labels:      map[string]string{"ignored": "yes"},
		Etag:        got.Etag,
	}).UpdateMask("description").Do()
	if err != nil {
		t.Fatalf("BackupVaults.Patch: %v", err)
	}

	e.wait(t, patchOp)

	patched := e.get(t, name)
	if patched.Description != "renamed" || patched.Labels["env"] != "dev" || patched.Labels["ignored"] != "" {
		t.Fatalf("masked patch changed the wrong fields: %+v", patched)
	}

	if patched.Etag == got.Etag || patched.UpdateTime == got.UpdateTime || patched.CreateTime != got.CreateTime {
		t.Fatalf("etag/updateTime not rotated or createTime moved: before=%+v after=%+v", got, patched)
	}

	// A stale etag is rejected with 409 ABORTED.
	_, err = e.svc.Projects.Locations.BackupVaults.Patch(name, &backupdr.BackupVault{
		Description: "lost update", Etag: got.Etag,
	}).UpdateMask("description").Do()
	wantCode(t, "stale-etag patch", err, http.StatusConflict)

	delOp, err := e.svc.Projects.Locations.BackupVaults.Delete(name).Etag(patched.Etag).Do()
	if err != nil {
		t.Fatalf("BackupVaults.Delete: %v", err)
	}

	e.wait(t, delOp)

	_, err = e.svc.Projects.Locations.BackupVaults.Get(name).Do()
	wantCode(t, "get after delete", err, http.StatusNotFound)
}

func assertCreated(t *testing.T, got *backupdr.BackupVault, name string) {
	t.Helper()

	if got.Name != name || got.Description != "primary vault" || got.Labels["env"] != "dev" {
		t.Fatalf("body not round-tripped: %+v", got)
	}

	if got.BackupMinimumEnforcedRetentionDuration != retention {
		t.Fatalf("retention = %q, want %q", got.BackupMinimumEnforcedRetentionDuration, retention)
	}

	if got.State != "ACTIVE" || !got.Deletable || got.AccessRestriction != "WITHIN_ORGANIZATION" {
		t.Fatalf("state/deletable/accessRestriction = %q/%v/%q", got.State, got.Deletable, got.AccessRestriction)
	}

	if got.BackupCount != 0 || got.TotalStoredBytes != 0 {
		t.Fatalf("usage = %d/%d, want 0/0", got.BackupCount, got.TotalStoredBytes)
	}

	if !strings.HasPrefix(got.ServiceAccount, "service-") ||
		!strings.HasSuffix(got.ServiceAccount, "@gcp-sa-backupdr-pr.iam.gserviceaccount.com") {
		t.Fatalf("serviceAccount = %q", got.ServiceAccount)
	}

	if got.Uid == "" || got.Etag == "" || got.CreateTime != "2026-03-04T05:06:07Z" || got.UpdateTime != got.CreateTime {
		t.Fatalf("uid/etag/timestamps = %q/%q/%q/%q", got.Uid, got.Etag, got.CreateTime, got.UpdateTime)
	}
}

// TestSDKBackupVaultListPaging lists across two pages with pageSize=1 and
// confirms filter/orderBy are accepted and the "-" location wildcard spans
// locations.
func TestSDKBackupVaultListPaging(t *testing.T) {
	e := newSDKEnv(t)

	for _, id := range []string{"vault-b", "vault-a"} {
		e.create(t, id, &backupdr.BackupVault{BackupMinimumEnforcedRetentionDuration: retention})
	}

	var names []string

	err := e.svc.Projects.Locations.BackupVaults.List(e.parent).PageSize(1).
		Filter(`state="ACTIVE"`).OrderBy("name").
		Pages(context.Background(), func(resp *backupdr.ListBackupVaultsResponse) error {
			if len(resp.BackupVaults) != 1 {
				t.Fatalf("page size = %d, want 1", len(resp.BackupVaults))
			}

			names = append(names, resp.BackupVaults[0].Name)

			return nil
		})
	if err != nil {
		t.Fatalf("List.Pages: %v", err)
	}

	if len(names) != 2 || !strings.HasSuffix(names[0], "/vault-a") || !strings.HasSuffix(names[1], "/vault-b") {
		t.Fatalf("paged names = %v, want [vault-a vault-b]", names)
	}

	all, err := e.svc.Projects.Locations.BackupVaults.List("projects/" + sdkProject + "/locations/-").Do()
	if err != nil || len(all.BackupVaults) != 2 {
		t.Fatalf("wildcard list = %+v (err %v)", all, err)
	}
}

// TestSDKBackupVaultValidation covers the 400/404/409 error paths.
func TestSDKBackupVaultValidation(t *testing.T) {
	e := newSDKEnv(t)
	vaults := e.svc.Projects.Locations.BackupVaults

	for _, bad := range []string{"", "one day", "-10s"} {
		_, err := vaults.Create(e.parent, &backupdr.BackupVault{
			BackupMinimumEnforcedRetentionDuration: bad,
		}).BackupVaultId("bad-retention").Do()
		wantCode(t, "create retention "+bad, err, http.StatusBadRequest)
	}

	e.create(t, "vault-v", &backupdr.BackupVault{BackupMinimumEnforcedRetentionDuration: retention})

	_, err := vaults.Create(e.parent, &backupdr.BackupVault{
		BackupMinimumEnforcedRetentionDuration: retention,
	}).BackupVaultId("vault-v").Do()
	wantCode(t, "duplicate create", err, http.StatusConflict)

	_, err = vaults.Get(e.parent + "/backupVaults/ghost").Do()
	wantCode(t, "get missing", err, http.StatusNotFound)

	name := e.parent + "/backupVaults/vault-v"

	for _, mask := range []string{"", "state", "bogusField"} {
		_, err = vaults.Patch(name, &backupdr.BackupVault{Description: "x"}).UpdateMask(mask).Do()
		wantCode(t, "patch mask "+mask, err, http.StatusBadRequest)
	}

	_, err = vaults.Patch(name, &backupdr.BackupVault{
		BackupMinimumEnforcedRetentionDuration: "-1s",
	}).UpdateMask("backupMinimumEnforcedRetentionDuration").Do()
	wantCode(t, "patch negative retention", err, http.StatusBadRequest)

	_, err = vaults.Patch(e.parent+"/backupVaults/ghost", &backupdr.BackupVault{Description: "x"}).
		UpdateMask("description").Do()
	wantCode(t, "patch missing", err, http.StatusNotFound)

	// An operation name nobody created is 404 from the shared poller.
	_, err = e.svc.Projects.Locations.Operations.Get(e.parent + "/operations/never-created").Do()
	wantCode(t, "poll unknown operation", err, http.StatusNotFound)
}

// TestSDKBackupVaultValidateOnly confirms validateOnly checks without storing.
func TestSDKBackupVaultValidateOnly(t *testing.T) {
	e := newSDKEnv(t)
	vaults := e.svc.Projects.Locations.BackupVaults

	op, err := vaults.Create(e.parent, &backupdr.BackupVault{
		BackupMinimumEnforcedRetentionDuration: retention,
	}).BackupVaultId("dry-run").ValidateOnly(true).RequestId("5f1c7c1e-1b1a-4d6e-9a53-6f0b6f2b8d11").Do()
	if err != nil {
		t.Fatalf("validateOnly create: %v", err)
	}

	// validateOnly mutates nothing, so it mints no pollable operation: the
	// reply is done with the would-be vault inline and no name.
	if !op.Done || op.Name != "" || len(op.Response) == 0 {
		t.Fatalf("validateOnly create op = %+v, want done, unnamed, with a response", op)
	}

	_, err = vaults.Get(e.parent + "/backupVaults/dry-run").Do()

	wantCode(t, "get after validateOnly create", err, http.StatusNotFound)

	_, err = vaults.Create(e.parent, &backupdr.BackupVault{}).BackupVaultId("dry-run").ValidateOnly(true).Do()
	wantCode(t, "validateOnly create without retention", err, http.StatusBadRequest)
}

// TestSDKBackupVaultDeleteGuards covers force (non-empty vault), allowMissing
// and a stale delete etag.
func TestSDKBackupVaultDeleteGuards(t *testing.T) {
	e := newSDKEnv(t)
	vaults := e.svc.Projects.Locations.BackupVaults
	name := e.parent + "/backupVaults/vault-full"

	e.create(t, "vault-full", &backupdr.BackupVault{BackupMinimumEnforcedRetentionDuration: retention})

	e.seedUsage(t, "vault-full", 2, 4096)

	full := e.get(t, name)
	if full.Deletable || full.BackupCount != 2 || full.TotalStoredBytes != 4096 {
		t.Fatalf("seeded usage not reported: %+v", full)
	}

	_, err := vaults.Delete(name).Do()
	wantCode(t, "delete non-empty without force", err, http.StatusBadRequest)

	_, err = vaults.Delete(name).Force(true).Etag("stale").Do()
	wantCode(t, "delete stale etag", err, http.StatusConflict)

	op, err := vaults.Delete(name).Force(true).IgnoreBackupPlanReferences(true).Do()
	if err != nil {
		t.Fatalf("force delete: %v", err)
	}

	e.wait(t, op)

	_, err = vaults.Delete(name).Do()
	wantCode(t, "delete missing", err, http.StatusNotFound)

	op, err = vaults.Delete(name).AllowMissing(true).Do()
	if err != nil {
		t.Fatalf("allowMissing delete: %v", err)
	}

	e.wait(t, op)
}

// seedUsage makes a vault non-empty through the provider's snapshot/restore
// seam (the emulator has no data plane that could create backups): it
// snapshots the Backup and DR state, sets the vault's backupCount and
// totalStoredBytes, and restores it.
func (e *sdkEnv) seedUsage(t *testing.T, id string, backupCount, totalStoredBytes int64) {
	t.Helper()

	ctx := context.Background()

	raw, err := e.cloud.BackupDR.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var snap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	var vaults map[string]bdrdriver.BackupVault
	if err := json.Unmarshal(snap["backupVaults"], &vaults); err != nil {
		t.Fatalf("decode snapshot vaults: %v", err)
	}

	key := e.parent + "/backupVaults/" + id

	v, ok := vaults[key]
	if !ok {
		t.Fatalf("seedUsage: vault %s not in snapshot", key)
	}

	v.BackupCount, v.TotalStoredBytes = backupCount, totalStoredBytes
	vaults[key] = v

	if snap["backupVaults"], err = json.Marshal(vaults); err != nil {
		t.Fatalf("encode vaults: %v", err)
	}

	if raw, err = json.Marshal(snap); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}

	if err := e.cloud.BackupDR.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}
