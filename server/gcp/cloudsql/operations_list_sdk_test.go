package cloudsql_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// Finding: writeErr rendered the wire error.message from err.Error(), which
// for a cloudemu *errors.Error prepends the internal code-name taxonomy (e.g.
// "NotFound: Cloud SQL instance ... not found") — real Cloud SQL never leaks
// that prefix into the message a client sees.
func TestSDKCloudSQLErrorMessageHasNoCodePrefix(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	_, err := svc.Instances.Get(project, "does-not-exist").Context(ctx).Do()

	var gerr *googleapi.Error
	if !isGoogleAPIError(err, &gerr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:"} {
		if strings.HasPrefix(gerr.Message, prefix) {
			t.Fatalf("error message %q leaks the internal code-name prefix %q", gerr.Message, prefix)
		}
	}

	if !strings.Contains(gerr.Message, "does-not-exist") {
		t.Fatalf("error message %q should still mention the missing instance", gerr.Message)
	}
}

func isGoogleAPIError(err error, out **googleapi.Error) bool {
	gerr, ok := err.(*googleapi.Error)
	if ok {
		*out = gerr
	}

	return ok
}

// Finding: buildOp keyed every recorded Operation on a caller-supplied name
// that was often a fixed action tag (e.g. "insert-db", "clone", "patch-{id}")
// shared across every call of that kind — a second patch of the SAME
// instance, or even an unrelated instance's database insert, silently
// overwrote the first Operation's record at the same map key. Real Cloud SQL
// hands back a distinct operation name per call.
func TestSDKCloudSQLOperationNamesAreUnique(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	insertInstance(t, svc, project, "collide")

	op1, err := svc.Instances.Patch(project, "collide", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch 1: %v", err)
	}

	op2, err := svc.Instances.Patch(project, "collide", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-4-16384"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch 2: %v", err)
	}

	if op1.Name == op2.Name {
		t.Fatalf("two patches on the same instance got the same operation name %q", op1.Name)
	}

	// The first operation's record must still be independently retrievable —
	// not clobbered by the second patch sharing its map key.
	if _, err := svc.Operations.Get(project, op1.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Operations.Get(op1.Name): %v", err)
	}

	if _, err := svc.Operations.Get(project, op2.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Operations.Get(op2.Name): %v", err)
	}
}

// Finding: GET /v1/projects/{p}/operations (operations.list, with no
// operation name) always 400'd with "operation name required" — real Cloud
// SQL supports listing the project's operations, optionally scoped to one
// instance via ?instance=.
func TestSDKCloudSQLOperationsList(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	insertInstance(t, svc, project, "opslist-a")
	insertInstance(t, svc, project, "opslist-b")

	if _, err := svc.Instances.Patch(project, "opslist-a", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-4-16384"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch opslist-a: %v", err)
	}

	all, err := svc.Operations.List(project).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.List (project-wide): %v", err)
	}

	if len(all.Items) < 3 {
		t.Fatalf("project-wide Operations.List returned %d items, want at least 3 (2 creates + 1 patch)", len(all.Items))
	}

	scoped, err := svc.Operations.List(project).Instance("opslist-a").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.List (instance-scoped): %v", err)
	}

	if len(scoped.Items) != 2 {
		t.Fatalf("instance-scoped Operations.List returned %d items, want 2 (create + patch of opslist-a)", len(scoped.Items))
	}

	for _, op := range scoped.Items {
		if op.OperationType != "CREATE" && op.OperationType != "UPDATE" {
			t.Fatalf("unexpected operationType %q in opslist-a's scoped list", op.OperationType)
		}
	}
}

// Finding: BackupRuns.list iterated the provider's raw memstore map (random
// Go iteration order) instead of a sorted view, so the reported order of
// backup runs was nondeterministic across calls — every other Cloud SQL list
// verb in this package returns a stable order.
func TestSDKCloudSQLBackupRunsListOrderIsDeterministic(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	insertInstance(t, svc, project, "bkorder")

	const n = 6

	var inserted []int64

	for i := 0; i < n; i++ {
		op, err := svc.BackupRuns.Insert(project, "bkorder", &sqladmin.BackupRun{}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("BackupRuns.Insert %d: %v", i, err)
		}

		id, err := strconv.ParseInt(op.TargetId, 10, 64)
		if err != nil {
			t.Fatalf("backup id %q not numeric: %v", op.TargetId, err)
		}

		inserted = append(inserted, id)
	}

	for attempt := 0; attempt < 5; attempt++ {
		list, err := svc.BackupRuns.List(project, "bkorder").Context(ctx).Do()
		if err != nil {
			t.Fatalf("BackupRuns.List (attempt %d): %v", attempt, err)
		}

		if len(list.Items) != n {
			t.Fatalf("attempt %d: got %d backup runs, want %d", attempt, len(list.Items), n)
		}

		for i, item := range list.Items {
			if item.Id != inserted[i] {
				t.Fatalf("attempt %d: backup run order = %v, want insertion order %v", attempt, idsOf(list.Items), inserted)
			}
		}
	}
}

func idsOf(items []*sqladmin.BackupRun) []int64 {
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.Id)
	}

	return out
}
