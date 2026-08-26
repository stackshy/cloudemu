package cloudsql_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// TestSDKCloudSQLPatchDataDiskType verifies a patched settings.dataDiskType is
// reflected on the following Get (previously double-dropped: the wire layer never
// mapped it onto the modify input and the backend never applied it).
func TestSDKCloudSQLPatchDataDiskType(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "disktype",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192", DataDiskType: "PD_SSD"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if _, err := svc.Instances.Patch(project, "disktype", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{DataDiskType: "PD_HDD"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Instances.Get(project, "disktype").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Settings.DataDiskType != "PD_HDD" {
		t.Fatalf("after patch dataDiskType = %q, want PD_HDD", got.Settings.DataDiskType)
	}

	// A patch that omits dataDiskType must not revert it (merge semantics).
	if _, err := svc.Instances.Patch(project, "disktype", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-4-16384"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch (tier only): %v", err)
	}

	got, err = svc.Instances.Get(project, "disktype").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after tier patch: %v", err)
	}

	if got.Settings.DataDiskType != "PD_HDD" {
		t.Fatalf("dataDiskType after unrelated patch = %q, want PD_HDD (preserved)", got.Settings.DataDiskType)
	}
}

// TestSDKCloudSQLDeletionProtection verifies deletionProtectionEnabled blocks a
// delete with HTTP 400 FAILED_PRECONDITION, and that clearing it via patch allows
// the delete to proceed.
func TestSDKCloudSQLDeletionProtection(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "protected",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:                      "db-custom-2-8192",
			DeletionProtectionEnabled: true,
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := svc.Instances.Get(project, "protected").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Settings == nil || !got.Settings.DeletionProtectionEnabled {
		t.Fatalf("deletionProtectionEnabled = %+v, want true", got.Settings)
	}

	// Delete is refused while protection is on.
	_, err = svc.Instances.Delete(project, "protected").Context(ctx).Do()
	if err == nil {
		t.Fatal("Delete of a protected instance: expected error")
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusBadRequest {
		t.Fatalf("Delete error = %v, want HTTP 400", err)
	}

	// Clearing protection requires ForceSendFields (false is otherwise omitted).
	if _, err := svc.Instances.Patch(project, "protected", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			DeletionProtectionEnabled: false,
			ForceSendFields:           []string{"DeletionProtectionEnabled"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch clearing protection: %v", err)
	}

	if _, err := svc.Instances.Delete(project, "protected").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete after clearing protection: %v", err)
	}
}

// TestSDKCloudSQLUpdateVsPatch verifies the PUT (Instances.Update) full-replace
// semantics differ from PATCH (Instances.Patch) merge semantics: an omitted
// setting reverts to its default on PUT but is preserved on PATCH.
func TestSDKCloudSQLUpdateVsPatch(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "replace",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:           "db-custom-4-16384",
			DataDiskSizeGb: 50,
			DataDiskType:   "PD_HDD",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// PATCH omitting dataDiskSizeGb/dataDiskType preserves them (merge).
	if _, err := svc.Instances.Patch(project, "replace", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	afterPatch, err := svc.Instances.Get(project, "replace").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if afterPatch.Settings.DataDiskSizeGb != 50 || afterPatch.Settings.DataDiskType != "PD_HDD" {
		t.Fatalf("after patch diskSize=%d diskType=%q, want 50 / PD_HDD (preserved)",
			afterPatch.Settings.DataDiskSizeGb, afterPatch.Settings.DataDiskType)
	}

	// PUT omitting dataDiskSizeGb/dataDiskType reverts them to defaults (replace).
	if _, err := svc.Instances.Update(project, "replace", &sqladmin.DatabaseInstance{
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-8-32768"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Update (PUT): %v", err)
	}

	afterPut, err := svc.Instances.Get(project, "replace").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if afterPut.Settings.Tier != "db-custom-8-32768" {
		t.Fatalf("after PUT tier = %q, want db-custom-8-32768", afterPut.Settings.Tier)
	}

	if afterPut.Settings.DataDiskSizeGb != 10 {
		t.Fatalf("after PUT dataDiskSizeGb = %d, want 10 (reverted to default)", afterPut.Settings.DataDiskSizeGb)
	}

	if afterPut.Settings.DataDiskType != "PD_SSD" {
		t.Fatalf("after PUT dataDiskType = %q, want PD_SSD (reverted to default)", afterPut.Settings.DataDiskType)
	}
}
