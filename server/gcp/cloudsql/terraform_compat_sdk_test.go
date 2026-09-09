package cloudsql_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newTestServer returns the raw httptest server URL plus a v1-client bound to it,
// so a test can both drive the SDK and probe the alternate REST prefixes gcloud
// and Terraform use.
func newTestServer(t *testing.T) (baseURL string, svc *sqladmin.Service) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudSQL: cloud.CloudSQL})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	var err error

	svc, err = sqladmin.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	return ts.URL, svc
}

// Finding: the handler only served /v1/projects/, so gcloud and the Terraform
// google provider — which hit /sql/v1beta4/projects/ and, for the generated
// database/user resources, the version-less /projects/ prefix — got a 501 and
// could never create an instance. All three prefixes must be served.
func TestCloudSQLServesAllRESTPrefixes(t *testing.T) {
	baseURL, svc := newTestServer(t)
	insertInstance(t, svc, "mock-project", "orders")

	prefixes := []string{
		"/v1/projects/mock-project/instances/orders",
		"/sql/v1beta4/projects/mock-project/instances/orders",
		"/projects/mock-project/instances/orders",
	}

	for _, p := range prefixes {
		resp, err := http.Get(baseURL + p) //nolint:noctx // short-lived test request.
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", p, resp.StatusCode)
		}
	}
}

// Finding: a Get omitted settings.storageAutoResize and settings.pricingPlan and
// the top-level instanceType/gceZone. Real Cloud SQL always reports them, so
// Terraform saw a perpetual disk_autoresize false->true and pricing_plan diff.
func TestCloudSQLGetReportsComputedDefaults(t *testing.T) {
	_, svc := newTestServer(t)
	ctx := context.Background()

	insertInstance(t, svc, "mock-project", "orders")

	got, err := svc.Instances.Get("mock-project", "orders").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.InstanceType != "CLOUD_SQL_INSTANCE" {
		t.Fatalf("instanceType = %q, want CLOUD_SQL_INSTANCE", got.InstanceType)
	}

	if got.GceZone == "" {
		t.Fatal("gceZone is empty, want a synthesized zone")
	}

	if got.Settings == nil {
		t.Fatal("settings is nil")
	}

	if got.Settings.PricingPlan != "PER_USE" {
		t.Fatalf("pricingPlan = %q, want PER_USE", got.Settings.PricingPlan)
	}

	// storageAutoResize defaults to true when the insert omits it.
	if got.Settings.StorageAutoResize == nil || !*got.Settings.StorageAutoResize {
		t.Fatalf("storageAutoResize = %v, want true", got.Settings.StorageAutoResize)
	}
}

// storageAutoResize must round-trip: a request that turns it off is honored and
// read back as false, rather than snapping back to the default.
func TestCloudSQLStorageAutoResizeRoundTrip(t *testing.T) {
	_, svc := newTestServer(t)
	ctx := context.Background()

	off := false
	if _, err := svc.Instances.Insert("mock-project", &sqladmin.DatabaseInstance{
		Name:            "noresize",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:              "db-custom-2-8192",
			StorageAutoResize: &off,
			// StorageAutoResize is a pointer bool; force it onto the wire even at
			// the zero value.
			ForceSendFields: []string{"StorageAutoResize"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := svc.Instances.Get("mock-project", "noresize").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Settings.StorageAutoResize == nil || *got.Settings.StorageAutoResize {
		t.Fatalf("storageAutoResize = %v, want false", got.Settings.StorageAutoResize)
	}
}
