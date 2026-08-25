package cloudsql_test

import (
	"context"
	"strings"
	"testing"

	sqladmin "google.golang.org/api/sqladmin/v1"
)

const selfLinkPrefix = "https://sqladmin.googleapis.com/sql/v1beta4/projects/"

// insertInstance is a small helper that creates a Postgres instance and fails
// the test on error.
func insertInstance(t *testing.T, svc *sqladmin.Service, project, name string) {
	t.Helper()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            name,
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Insert %s: %v", name, err)
	}
}

// Finding: connectionName was built from the server's configured project
// (mock-project), ignoring the request project. Real Cloud SQL keys it on the
// request project as {project}:{region}:{instance}.
func TestSDKCloudSQLConnectionNameUsesRequestProject(t *testing.T) {
	svc, _ := newSDKClient(t)
	ctx := context.Background()

	const project = "proj"

	insertInstance(t, svc, project, "pg")

	got, err := svc.Instances.Get(project, "pg").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if want := "proj:us-central1:pg"; got.ConnectionName != want {
		t.Fatalf("connectionName = %q, want %q", got.ConnectionName, want)
	}

	if strings.HasPrefix(got.ConnectionName, "mock-project:") {
		t.Fatalf("connectionName %q still keyed on the server project", got.ConnectionName)
	}
}

// Finding: selfLink was a relative, wrong-version path (/v1/projects/...).
// Real Cloud SQL returns the absolute sql/v1beta4 URL, for instances and
// databases alike.
func TestSDKCloudSQLSelfLinkIsAbsolute(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	insertInstance(t, svc, project, "orders")

	got, err := svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if want := selfLinkPrefix + project + "/instances/orders"; got.SelfLink != want {
		t.Fatalf("instance selfLink = %q, want %q", got.SelfLink, want)
	}

	if _, err := svc.Databases.Insert(project, "orders", &sqladmin.Database{
		Name: "appdb",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Databases.Insert: %v", err)
	}

	db, err := svc.Databases.Get(project, "orders", "appdb").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Databases.Get: %v", err)
	}

	if want := selfLinkPrefix + project + "/instances/orders/databases/appdb"; db.SelfLink != want {
		t.Fatalf("database selfLink = %q, want %q", db.SelfLink, want)
	}
}

// Finding: instances.list ignored maxResults, pageToken and filter and never
// returned a nextPageToken.
func TestSDKCloudSQLListPaginationAndFilter(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"aaa1", "aaa2", "bbb1", "bbb2"} {
		insertInstance(t, svc, project, name)
	}

	// Page 1: two items plus a continuation token.
	page1, err := svc.Instances.List(project).MaxResults(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}

	if len(page1.Items) != 2 {
		t.Fatalf("page 1 items = %d, want 2", len(page1.Items))
	}

	if page1.NextPageToken == "" {
		t.Fatal("expected a nextPageToken on page 1")
	}

	// Page 2: the remaining two, no further token.
	page2, err := svc.Instances.List(project).MaxResults(2).PageToken(page1.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if len(page2.Items) != 2 {
		t.Fatalf("page 2 items = %d, want 2", len(page2.Items))
	}

	if page2.NextPageToken != "" {
		t.Fatalf("page 2 nextPageToken = %q, want empty", page2.NextPageToken)
	}

	// Filter narrows to the two aaa* instances.
	filtered, err := svc.Instances.List(project).Filter("name:aaa").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}

	if len(filtered.Items) != 2 {
		t.Fatalf("filtered items = %d, want 2", len(filtered.Items))
	}

	for _, it := range filtered.Items {
		if !strings.Contains(it.Name, "aaa") {
			t.Fatalf("filter returned non-matching instance %q", it.Name)
		}
	}
}

// Finding: Operations.Get returned a synthetic operationType=GET with empty
// target fields, and the insert LRO left selfLink/timestamps/user empty. Real
// Cloud SQL persists the CREATE/UPDATE/DELETE record pointing at the affected
// instance.
func TestSDKCloudSQLOperationsGetReturnsRealRecord(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	op, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "acct",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// The inline LRO envelope carries the full record.
	assertCreateOp(t, op)

	// Operations.Get returns the same persisted record, not a synthetic GET.
	got, err := svc.Operations.Get(project, op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	assertCreateOp(t, got)

	// An unknown operation is a 404, not a synthetic DONE.
	if _, err := svc.Operations.Get(project, "no-such-op").Context(ctx).Do(); err == nil {
		t.Fatal("expected error for unknown operation, got nil")
	}
}

func assertCreateOp(t *testing.T, op *sqladmin.Operation) {
	t.Helper()

	if op.OperationType != "CREATE" {
		t.Fatalf("operationType = %q, want CREATE", op.OperationType)
	}

	if op.TargetId != "acct" {
		t.Fatalf("targetId = %q, want acct", op.TargetId)
	}

	if !strings.HasPrefix(op.SelfLink, selfLinkPrefix) {
		t.Fatalf("operation selfLink = %q, want absolute sql/v1beta4 URL", op.SelfLink)
	}

	if !strings.HasPrefix(op.TargetLink, selfLinkPrefix) {
		t.Fatalf("operation targetLink = %q, want absolute sql/v1beta4 URL", op.TargetLink)
	}

	if op.InsertTime == "" || op.StartTime == "" || op.EndTime == "" || op.User == "" {
		t.Fatalf("operation missing populated fields: insert=%q start=%q end=%q user=%q",
			op.InsertTime, op.StartTime, op.EndTime, op.User)
	}
}
