package metastore_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	metastore "google.golang.org/api/metastore/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*metastore.APIService, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := metastore.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("metastore.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKServiceLifecycle drives the full metastore-service lifecycle through the
// real google.golang.org/api/metastore/v1 client: create (LRO), poll, get, list,
// patch, delete. The create leaves port, databaseType, releaseChannel, and tier
// UNSET, and the test asserts the emulator fills the real API defaults and
// reports them (plus the output-only state/endpointUri/artifactGcsUri/uid/
// createTime) stably — the exact behavior a Terraform plan needs to converge,
// since those attributes are computed. The nested telemetryConfig/
// hiveMetastoreConfig blocks are Optional non-Computed in the provider, so they
// are left absent when the client omits them (asserted below) — injecting a
// default there is the one thing that drifts a plan.
func TestSDKServiceLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/services/ms"

	// A DEVELOPER-tier service leaving every server-defaulted field unset.
	want := &metastore.Service{
		Tier:    "DEVELOPER",
		Network: "projects/" + project + "/global/networks/default",
	}

	op, err := svc.Projects.Locations.Services.Create(parent, want).
		ServiceId("ms").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Create: %v", err)
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

	got, err := svc.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Get: %v", err)
	}

	if got.Name != name {
		t.Fatalf("name = %q, want %q", got.Name, name)
	}

	// Output-only state seeded ACTIVE so Terraform reconciles clean.
	if got.State != "ACTIVE" {
		t.Fatalf("state = %q, want ACTIVE", got.State)
	}

	// The classic drift point: server-defaulted fields the client omitted must
	// come back filled with the real API defaults, identically on every read.
	if got.Port != 9083 {
		t.Fatalf("port default not filled: %d, want 9083", got.Port)
	}

	if got.DatabaseType != "MYSQL" {
		t.Fatalf("databaseType default not filled: %q, want MYSQL", got.DatabaseType)
	}

	if got.ReleaseChannel != "STABLE" {
		t.Fatalf("releaseChannel default not filled: %q, want STABLE", got.ReleaseChannel)
	}

	// The nested Optional-non-Computed blocks are NOT injected when omitted, so a
	// Terraform plan that declares no such block stays clean.
	if got.TelemetryConfig != nil {
		t.Fatalf("telemetryConfig injected when omitted (would drift a plan): %+v", got.TelemetryConfig)
	}

	if got.HiveMetastoreConfig != nil {
		t.Fatalf("hiveMetastoreConfig injected when omitted (would drift a plan): %+v", got.HiveMetastoreConfig)
	}

	// Deterministic computed identity fields.
	if got.Uid == "" || got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed identity fields missing: uid=%q create=%q update=%q", got.Uid, got.CreateTime, got.UpdateTime)
	}

	if !strings.HasPrefix(got.EndpointUri, "thrift://") {
		t.Fatalf("endpointUri not derived: %q", got.EndpointUri)
	}

	if !strings.HasPrefix(got.ArtifactGcsUri, "gs://") || !strings.HasSuffix(got.ArtifactGcsUri, "/hive-metastore") {
		t.Fatalf("artifactGcsUri not derived: %q", got.ArtifactGcsUri)
	}

	// Stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Services.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Port != got.Port || second.DatabaseType != got.DatabaseType ||
		second.ReleaseChannel != got.ReleaseChannel || second.State != got.State ||
		second.Uid != got.Uid || second.EndpointUri != got.EndpointUri ||
		second.ArtifactGcsUri != got.ArtifactGcsUri || second.CreateTime != got.CreateTime {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// List.
	list, err := svc.Projects.Locations.Services.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Services) != 1 || !strings.HasSuffix(list.Services[0].Name, "/ms") {
		t.Fatalf("list = %+v", list.Services)
	}

	// Patch labels via updateMask -> LRO, applied; tier/network untouched.
	patchOp, err := svc.Projects.Locations.Services.Patch(name, &metastore.Service{
		Labels: map[string]string{"env": "prod"},
	}).UpdateMask("labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Services.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Labels["env"] != "prod" {
		t.Fatalf("labels after patch = %+v, want env=prod", updated.Labels)
	}

	if updated.Tier != "DEVELOPER" {
		t.Fatalf("tier mutated by masked patch: %q", updated.Tier)
	}

	// The defaulted fields survive an unrelated masked patch, still stable.
	if updated.Port != 9083 || updated.DatabaseType != "MYSQL" || updated.State != "ACTIVE" {
		t.Fatalf("defaulted fields disturbed by patch: port=%d db=%q state=%q", updated.Port, updated.DatabaseType, updated.State)
	}

	delOp, err := svc.Projects.Locations.Services.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Services.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKServiceExplicitValuesPreserved verifies a client-supplied value for a
// server-defaulted field is never overwritten by the default (only an absent
// field is filled), and that a rich nested block (hiveMetastoreConfig) round
// trips verbatim with its version preserved.
func TestSDKServiceExplicitValuesPreserved(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/services/custom"

	want := &metastore.Service{
		Tier:           "ENTERPRISE",
		Port:           9084,
		DatabaseType:   "SPANNER",
		ReleaseChannel: "CANARY",
		TelemetryConfig: &metastore.TelemetryConfig{
			LogFormat: "LEGACY",
		},
		HiveMetastoreConfig: &metastore.HiveMetastoreConfig{
			Version:         "3.1.2",
			ConfigOverrides: map[string]string{"hive.metastore.warehouse.dir": "/tmp/warehouse"},
		},
	}

	if _, err := svc.Projects.Locations.Services.Create(parent, want).ServiceId("custom").Do(); err != nil {
		t.Fatalf("Create (explicit values): %v", err)
	}

	got, err := svc.Projects.Locations.Services.Get(name).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Port != 9084 || got.DatabaseType != "SPANNER" || got.ReleaseChannel != "CANARY" || got.Tier != "ENTERPRISE" {
		t.Fatalf("explicit values overwritten by defaults: %+v", got)
	}

	if got.TelemetryConfig.LogFormat != "LEGACY" {
		t.Fatalf("explicit logFormat overwritten: %q", got.TelemetryConfig.LogFormat)
	}

	if got.HiveMetastoreConfig.Version != "3.1.2" ||
		got.HiveMetastoreConfig.ConfigOverrides["hive.metastore.warehouse.dir"] != "/tmp/warehouse" {
		t.Fatalf("hiveMetastoreConfig not round-tripped verbatim: %+v", got.HiveMetastoreConfig)
	}

	// endpointUri reflects the caller-supplied port.
	if !strings.HasSuffix(got.EndpointUri, ":9084") {
		t.Fatalf("endpointUri does not reflect explicit port: %q", got.EndpointUri)
	}
}

func TestSDKServiceNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Services.Get(parent + "/services/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	c := &metastore.Service{Tier: "DEVELOPER"}

	if _, err := svc.Projects.Locations.Services.Create(parent, c).ServiceId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Services.Create(parent, c).ServiceId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
