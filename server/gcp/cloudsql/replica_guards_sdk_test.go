package cloudsql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// insertReplica creates a read replica of master via a normal insert carrying
// masterInstanceName, matching how a real sqladmin client provisions a replica.
func insertReplica(t *testing.T, svc *sqladmin.Service, project, name, master string) {
	t.Helper()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:               name,
		DatabaseVersion:    "POSTGRES_15",
		Region:             "us-central1",
		MasterInstanceName: master,
		Settings:           &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("replica Insert %q: %v", name, err)
	}
}

// requireAPIError asserts err is a googleapi.Error with the wanted HTTP status
// whose message mentions want, so the test pins the real wire status a client
// sees (Cloud SQL maps these FAILED_PRECONDITION guards to HTTP 400).
func requireAPIError(t *testing.T, err error, code int, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected an error mentioning %q, got nil", want)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	if gerr.Code != code {
		t.Fatalf("error code = %d, want %d (%v)", gerr.Code, code, err)
	}

	if !strings.Contains(gerr.Message, want) {
		t.Fatalf("error message = %q, want it to mention %q", gerr.Message, want)
	}
}

// TestSDKCloudSQLDeletePrimaryWithReplicaBlocked drives the real sqladmin SDK:
// deleting a primary that still has a read replica is rejected, and only
// succeeds once the replica is removed first — matching real Cloud SQL, which
// refuses to orphan replicas.
func TestSDKCloudSQLDeletePrimaryWithReplicaBlocked(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	mustCreateInstance(t, svc, project, "pg")
	insertReplica(t, svc, project, "pg-replica", "pg")

	// The primary cannot be deleted while a replica is attached.
	_, err := svc.Instances.Delete(project, "pg").Context(ctx).Do()
	requireAPIError(t, err, 400, "replica")

	// The primary is still present after the blocked delete.
	if _, err := svc.Instances.Get(project, "pg").Context(ctx).Do(); err != nil {
		t.Fatalf("primary should survive a blocked delete: %v", err)
	}

	// Deleting the replica first is allowed.
	if _, err := svc.Instances.Delete(project, "pg-replica").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete replica: %v", err)
	}

	// With the replica gone, the primary deletes cleanly.
	if _, err := svc.Instances.Delete(project, "pg").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete primary after replica removed: %v", err)
	}
}

// TestSDKCloudSQLStopReplicaGuards asserts the real Cloud SQL start/stop rules
// over the wire: an instance that has read replicas cannot be stopped, an
// instance that is itself a read replica cannot be stopped, and once the replica
// is removed the primary stops normally. Stop is emulated by a Patch that sets
// settings.activationPolicy=NEVER.
func TestSDKCloudSQLStopReplicaGuards(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	mustCreateInstance(t, svc, project, "pg")
	insertReplica(t, svc, project, "pg-replica", "pg")

	stop := func(name string) error {
		_, err := svc.Instances.Patch(project, name, &sqladmin.DatabaseInstance{
			Settings: &sqladmin.Settings{ActivationPolicy: "NEVER"},
		}).Context(ctx).Do()

		return err
	}

	// A primary that still has a replica cannot be stopped.
	requireAPIError(t, stop("pg"), 400, "read replicas")

	// A read replica cannot be stopped on its own.
	requireAPIError(t, stop("pg-replica"), 400, "read replica")

	// Remove the replica, then the primary stops cleanly.
	if _, err := svc.Instances.Delete(project, "pg-replica").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete replica: %v", err)
	}

	if err := stop("pg"); err != nil {
		t.Fatalf("stop primary after replica removed: %v", err)
	}

	got, err := svc.Instances.Get(project, "pg").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get primary: %v", err)
	}

	// Cloud SQL reports a stopped (activationPolicy=NEVER) instance as SUSPENDED.
	if got.State != "SUSPENDED" {
		t.Fatalf("primary state = %q, want SUSPENDED", got.State)
	}
}

// TestSDKCloudSQLDeletionProtectionBlocksDelete confirms the deletion-protection
// guard end to end: a protected instance refuses delete with the real error, and
// clearing settings.deletionProtectionEnabled lets the delete through.
func TestSDKCloudSQLDeletionProtectionBlocksDelete(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "guarded",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:                      "db-custom-2-8192",
			DeletionProtectionEnabled: true,
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	_, err := svc.Instances.Delete(project, "guarded").Context(ctx).Do()
	requireAPIError(t, err, 400, "deletion protection")

	// Clear the guard (false is the zero value, so force it onto the wire).
	if _, err := svc.Instances.Patch(project, "guarded", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			DeletionProtectionEnabled: false,
			ForceSendFields:           []string{"DeletionProtectionEnabled"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Patch clear protection: %v", err)
	}

	if _, err := svc.Instances.Delete(project, "guarded").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete after clearing protection: %v", err)
	}
}
