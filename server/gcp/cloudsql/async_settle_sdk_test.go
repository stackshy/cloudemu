package cloudsql_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	cloudsqlprovider "github.com/stackshy/cloudemu/v2/providers/gcp/cloudsql"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newAsyncSDKClient stands up the Cloud SQL wire handler over a provider with
// AsyncSettle enabled and a FakeClock, so a real sqladmin SDK client observes
// the transient PENDING_CREATE state before the settle window elapses.
func newAsyncSDKClient(t *testing.T) (*sqladmin.Service, *config.FakeClock, string) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
		config.WithAsyncSettle(),
	)

	srv := gcpserver.New(gcpserver.Drivers{CloudSQL: cloudsqlprovider.New(opts)})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := sqladmin.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	return svc, fc, "mock-project"
}

// TestSDKCloudSQLAsyncSettle drives the real sqladmin SDK against the wire
// handler and asserts the create/patch transitions surface real Cloud SQL
// states: PENDING_CREATE → RUNNABLE on insert, MAINTENANCE → RUNNABLE on patch.
func TestSDKCloudSQLAsyncSettle(t *testing.T) {
	svc, fc, project := newAsyncSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "orders",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	got, err := svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get before settle: %v", err)
	}

	if got.State != "PENDING_CREATE" {
		t.Fatalf("state before settle = %q, want PENDING_CREATE", got.State)
	}

	fc.Advance(settle.DefaultCloudSQLSettle + time.Second)

	got, err = svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get after settle: %v", err)
	}

	if got.State != "RUNNABLE" {
		t.Fatalf("state after settle = %q, want RUNNABLE", got.State)
	}

	// Patch → MAINTENANCE → RUNNABLE.
	if _, err := svc.Instances.Patch(project, "orders", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{Tier: "db-custom-4-16384"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Patch: %v", err)
	}

	got, err = svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get during patch: %v", err)
	}

	if got.State != "MAINTENANCE" {
		t.Fatalf("state during patch = %q, want MAINTENANCE", got.State)
	}

	fc.Advance(settle.DefaultCloudSQLSettle + time.Second)

	got, err = svc.Instances.Get(project, "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get after patch settle: %v", err)
	}

	if got.State != "RUNNABLE" {
		t.Fatalf("state after patch settle = %q, want RUNNABLE", got.State)
	}
}
