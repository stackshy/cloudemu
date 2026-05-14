package cloudsql_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

func newSDKClient(t *testing.T) (*sqladmin.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{
		CloudSQL: cloud.CloudSQL,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := sqladmin.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKCloudSQLCreateGetList(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	op, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "orders",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:           "db-custom-2-8192",
			DataDiskSizeGb: 50,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	if op.Status != "DONE" {
		t.Fatalf("got op status %q, want DONE", op.Status)
	}

	got, err := svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if got.Name != "orders" {
		t.Fatalf("got name %q, want orders", got.Name)
	}

	if got.State != "RUNNABLE" {
		t.Fatalf("got state %q, want RUNNABLE", got.State)
	}

	if got.ConnectionName == "" {
		t.Fatal("expected ConnectionName to be set")
	}

	list, err := svc.Instances.List(project).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("got %d instances, want 1", len(list.Items))
	}
}

func TestSDKCloudSQLLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	_, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "life",
		DatabaseVersion: "MYSQL_8_0",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-f1-micro"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Stop via patch with activationPolicy=NEVER.
	_, err = svc.Instances.Patch(project, "life", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{ActivationPolicy: "NEVER"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch (stop): %v", err)
	}

	got, err := svc.Instances.Get(project, "life").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}

	if got.State != "SUSPENDED" {
		t.Fatalf("after stop got state %q, want SUSPENDED", got.State)
	}

	// Start via patch with activationPolicy=ALWAYS.
	_, err = svc.Instances.Patch(project, "life", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{ActivationPolicy: "ALWAYS"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch (start): %v", err)
	}

	// Restart.
	if _, err := svc.Instances.Restart(project, "life").Context(ctx).Do(); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// Delete.
	if _, err := svc.Instances.Delete(project, "life").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Instances.Get(project, "life").Context(ctx).Do(); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestSDKCloudSQLBackupRunsAndRestore(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "src",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192", DataDiskSizeGb: 30},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert src: %v", err)
	}

	op, err := svc.BackupRuns.Insert(project, "src", &sqladmin.BackupRun{
		Description: "test backup",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackupRuns.Insert: %v", err)
	}

	if op.Status != "DONE" {
		t.Fatalf("backup op status %q, want DONE", op.Status)
	}

	backupID := op.TargetId

	if backupID == "" {
		t.Fatal("expected TargetId from BackupRuns.Insert response")
	}

	list, err := svc.BackupRuns.List(project, "src").Context(ctx).Do()
	if err != nil {
		t.Fatalf("BackupRuns.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("got %d backup runs, want 1", len(list.Items))
	}

	// Create the target instance for restore (Cloud SQL: target must exist).
	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "target",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert target: %v", err)
	}

	// RestoreBackup is special: target instance is the URL path; backup run
	// id is in the body. Real Cloud SQL replaces the target's data with the
	// backup; the mock just verifies the operation completes successfully.
	// We use the existing target instance — RestoreBackup is supposed to be
	// applied to an *existing* instance.
	parsedID, err := strconvAtoi64(backupID)
	if err != nil {
		t.Fatalf("backup id %q not numeric: %v", backupID, err)
	}

	// Delete the target so the restore (which uses NewInstanceID for our
	// mock) doesn't conflict — RestoreBackup conceptually replaces the
	// target's data.
	if _, err := svc.Instances.Delete(project, "target").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete target before restore: %v", err)
	}

	if _, err := svc.Instances.RestoreBackup(project, "target",
		&sqladmin.InstancesRestoreBackupRequest{
			RestoreBackupContext: &sqladmin.RestoreBackupContext{
				BackupRunId: parsedID,
				InstanceId:  "src",
			},
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.RestoreBackup: %v", err)
	}

	if _, err := svc.BackupRuns.Delete(project, "src", parsedID).Context(ctx).Do(); err != nil {
		t.Fatalf("BackupRuns.Delete: %v", err)
	}
}

// strconvAtoi64 parses a decimal int64 without dragging in strconv at file
// scope (and because BackupRun IDs are int64 in the SDK).
func strconvAtoi64(s string) (int64, error) {
	var n int64

	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, &parseErr{s: s}
		}

		n = n*10 + int64(ch-'0')
	}

	return n, nil
}

type parseErr struct{ s string }

func (e *parseErr) Error() string { return "not a number: " + e.s }
