package gkebackup_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gkebackup "google.golang.org/api/gkebackup/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*gkebackup.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := gkebackup.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gkebackup.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKBackupPlanLifecycle exercises the full backupPlan surface: a create
// with a cluster reference, backupConfig, backupSchedule, and retentionPolicy;
// the computed uid/etag/state/timestamps; their byte-stability across reads (the
// Terraform drift point); and a masked patch of labels/description.
func TestSDKBackupPlanLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/backupPlans/plan"
	cluster := parent + "/clusters/prod"

	op, err := svc.Projects.Locations.BackupPlans.Create(parent, &gkebackup.BackupPlan{
		Cluster:     cluster,
		Description: "nightly",
		Labels:      map[string]string{"team": "platform"},
		BackupConfig: &gkebackup.BackupConfig{
			AllNamespaces:     true,
			IncludeVolumeData: true,
			IncludeSecrets:    true,
		},
		BackupSchedule:  &gkebackup.Schedule{CronSchedule: "0 3 * * *"},
		RetentionPolicy: &gkebackup.RetentionPolicy{BackupDeleteLockDays: 1, BackupRetainDays: 30},
	}).BackupPlanId("plan").Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackupPlans.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done (would hang a Terraform apply)")
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	got, err := svc.Projects.Locations.BackupPlans.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackupPlans.Get: %v", err)
	}

	if got.Name != name || got.Uid == "" || got.Etag == "" || got.State != "READY" ||
		got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed fields wrong: %+v", got)
	}

	if got.Cluster != cluster || got.Description != "nightly" || got.Labels["team"] != "platform" {
		t.Fatalf("inputs not round-tripped: %+v", got)
	}

	if got.BackupConfig == nil || !got.BackupConfig.AllNamespaces ||
		got.BackupSchedule == nil || got.BackupSchedule.CronSchedule != "0 3 * * *" ||
		got.RetentionPolicy == nil || got.RetentionPolicy.BackupRetainDays != 30 {
		t.Fatalf("config blocks not round-tripped: %+v", got)
	}

	// Stable across reads (no Terraform drift on the computed identity).
	second, err := svc.Projects.Locations.BackupPlans.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Uid != got.Uid || second.Etag != got.Etag || second.State != got.State ||
		second.CreateTime != got.CreateTime || second.UpdateTime != got.UpdateTime {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// Patch description + labels via updateMask -> LRO; config blocks untouched, etag rotates.
	patchOp, err := svc.Projects.Locations.BackupPlans.Patch(name, &gkebackup.BackupPlan{
		Description: "nightly v2",
		Labels:      map[string]string{"team": "platform", "env": "prod"},
	}).UpdateMask("description,labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackupPlans.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.BackupPlans.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "nightly v2" || updated.Labels["env"] != "prod" {
		t.Fatalf("patch not applied: %+v", updated)
	}

	if updated.Uid != got.Uid || updated.Etag == got.Etag {
		t.Fatalf("uid must be stable and etag must rotate on mutation: %+v", updated)
	}

	if updated.BackupSchedule == nil || updated.BackupSchedule.CronSchedule != "0 3 * * *" || updated.Cluster != cluster {
		t.Fatalf("masked patch mutated unmentioned fields: %+v", updated)
	}

	delOp, err := svc.Projects.Locations.BackupPlans.Delete(name).Do()
	if err != nil {
		t.Fatalf("BackupPlans.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.BackupPlans.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKBackupPlanDeactivatedState verifies a deactivated backupPlan reports
// state DEACTIVATED, and toggling deactivated via a masked patch flips the state.
func TestSDKBackupPlanDeactivatedState(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/backupPlans/off"

	if _, err := svc.Projects.Locations.BackupPlans.Create(parent, &gkebackup.BackupPlan{
		Cluster:         parent + "/clusters/prod",
		Deactivated:     true,
		ForceSendFields: []string{"Deactivated"},
	}).BackupPlanId("off").Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.BackupPlans.Get(name).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.State != "DEACTIVATED" {
		t.Fatalf("state = %q, want DEACTIVATED", got.State)
	}

	if _, err := svc.Projects.Locations.BackupPlans.Patch(name, &gkebackup.BackupPlan{
		Deactivated:     false,
		ForceSendFields: []string{"Deactivated"},
	}).UpdateMask("deactivated").Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	reactivated, err := svc.Projects.Locations.BackupPlans.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if reactivated.State != "READY" {
		t.Fatalf("state after reactivation = %q, want READY", reactivated.State)
	}
}

// TestSDKRestorePlanLifecycle covers the restorePlan collection referencing a
// backupPlan, its computed identity, verbatim restoreConfig round-trip, and list.
func TestSDKRestorePlanLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/restorePlans/restore"
	backupPlan := parent + "/backupPlans/plan"
	cluster := parent + "/clusters/dr"

	op, err := svc.Projects.Locations.RestorePlans.Create(parent, &gkebackup.RestorePlan{
		BackupPlan:  backupPlan,
		Cluster:     cluster,
		Description: "dr restore",
		Labels:      map[string]string{"purpose": "dr"},
		RestoreConfig: &gkebackup.RestoreConfig{
			AllNamespaces:                 true,
			ClusterResourceConflictPolicy: "USE_EXISTING_VERSION",
			NamespacedResourceRestoreMode: "DELETE_AND_RESTORE",
			VolumeDataRestorePolicy:       "RESTORE_VOLUME_DATA_FROM_BACKUP",
		},
	}).RestorePlanId("restore").Do()
	if err != nil {
		t.Fatalf("RestorePlans.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.RestorePlans.Get(name).Do()
	if err != nil {
		t.Fatalf("RestorePlans.Get: %v", err)
	}

	if got.Name != name || got.Uid == "" || got.Etag == "" || got.State != "READY" || got.CreateTime == "" {
		t.Fatalf("computed fields wrong: %+v", got)
	}

	if got.BackupPlan != backupPlan || got.Cluster != cluster || got.RestoreConfig == nil ||
		got.RestoreConfig.ClusterResourceConflictPolicy != "USE_EXISTING_VERSION" ||
		got.RestoreConfig.VolumeDataRestorePolicy != "RESTORE_VOLUME_DATA_FROM_BACKUP" {
		t.Fatalf("restoreConfig not round-tripped: %+v", got)
	}

	list, err := svc.Projects.Locations.RestorePlans.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.RestorePlans) != 1 || !strings.HasSuffix(list.RestorePlans[0].Name, "/restore") {
		t.Fatalf("list = %+v", list.RestorePlans)
	}
}

// TestSDKNotFoundAndDuplicate covers 404 and 409 on the backupPlans collection.
func TestSDKNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.BackupPlans.Get(parent + "/backupPlans/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	plan := &gkebackup.BackupPlan{Cluster: parent + "/clusters/c"}

	if _, err := svc.Projects.Locations.BackupPlans.Create(parent, plan).BackupPlanId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.BackupPlans.Create(parent, plan).BackupPlanId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
