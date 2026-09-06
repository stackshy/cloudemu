package composer_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	composer "google.golang.org/api/composer/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*composer.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := composer.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("composer.NewService: %v", err)
	}

	return svc, "mock-project"
}

func TestSDKComposerLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	location := "us-central1"
	parent := "projects/" + project + "/locations/" + location
	name := parent + "/environments/analytics"

	want := &composer.Environment{
		Name:   name,
		Labels: map[string]string{"env": "test"},
		Config: &composer.EnvironmentConfig{
			EnvironmentSize: "ENVIRONMENT_SIZE_SMALL",
			SoftwareConfig:  &composer.SoftwareConfig{ImageVersion: "composer-2.9.7-airflow-2.9.3"},
			NodeConfig: &composer.NodeConfig{
				Network:    "projects/mock-project/global/networks/default",
				Subnetwork: "projects/mock-project/regions/us-central1/subnetworks/default",
			},
			// An unmodeled sub-block, to prove verbatim passthrough round-trip.
			PrivateEnvironmentConfig: &composer.PrivateEnvironmentConfig{EnablePrivateEnvironment: true},
		},
	}

	// Create -> completed LRO carrying the environment, resolved immediately.
	op, err := svc.Projects.Locations.Environments.Create(parent, want).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done (would hang a Terraform apply)")
	}

	// Poll the location-scoped operation via the shared LRO poller -> done.
	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	// Get -> RUNNING, computed outputs, deep config round-trip.
	got, err := svc.Projects.Locations.Environments.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.Get: %v", err)
	}

	assertComputed(t, got, location)

	if got.Config.SoftwareConfig.ImageVersion != "composer-2.9.7-airflow-2.9.3" {
		t.Fatalf("imageVersion = %q", got.Config.SoftwareConfig.ImageVersion)
	}

	if got.Config.EnvironmentSize != "ENVIRONMENT_SIZE_SMALL" {
		t.Fatalf("environmentSize = %q", got.Config.EnvironmentSize)
	}

	if got.Config.NodeConfig == nil || !strings.HasSuffix(got.Config.NodeConfig.Network, "/default") {
		t.Fatalf("nodeConfig.network not round-tripped: %+v", got.Config.NodeConfig)
	}

	if got.Config.PrivateEnvironmentConfig == nil || !got.Config.PrivateEnvironmentConfig.EnablePrivateEnvironment {
		t.Fatalf("unmodeled privateEnvironmentConfig not round-tripped verbatim")
	}

	if got.Labels["env"] != "test" {
		t.Fatalf("labels not round-tripped")
	}

	assertStableAcrossGets(t, svc, name, got)

	// List.
	list, err := svc.Projects.Locations.Environments.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.List: %v", err)
	}

	if len(list.Environments) != 1 || !strings.HasSuffix(list.Environments[0].Name, "/analytics") {
		t.Fatalf("list = %+v", list.Environments)
	}

	// Patch imageVersion via updateMask -> completed LRO, applied.
	patchOp, err := svc.Projects.Locations.Environments.Patch(name, &composer.Environment{
		Config: &composer.EnvironmentConfig{
			SoftwareConfig: &composer.SoftwareConfig{ImageVersion: "composer-2.9.8-airflow-2.9.3"},
		},
	}).UpdateMask("config.softwareConfig.imageVersion").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Environments.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.Get after patch: %v", err)
	}

	if updated.Config.SoftwareConfig.ImageVersion != "composer-2.9.8-airflow-2.9.3" {
		t.Fatalf("imageVersion after patch = %q", updated.Config.SoftwareConfig.ImageVersion)
	}

	// A field outside the mask is untouched.
	if updated.Config.EnvironmentSize != "ENVIRONMENT_SIZE_SMALL" {
		t.Fatalf("environmentSize mutated by masked patch: %q", updated.Config.EnvironmentSize)
	}

	// Delete -> completed LRO, then a Get 404s.
	delOp, err := svc.Projects.Locations.Environments.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Environments.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Environments.Get(name).Context(ctx).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// assertComputed verifies every deterministic output field is populated.
func assertComputed(t *testing.T, got *composer.Environment, location string) {
	t.Helper()

	if got.State != "RUNNING" {
		t.Fatalf("state = %q, want RUNNING", got.State)
	}

	if got.Uuid == "" {
		t.Fatalf("uuid empty")
	}

	if got.CreateTime == "" {
		t.Fatalf("createTime empty")
	}

	if got.Config.GkeCluster == "" || !strings.Contains(got.Config.GkeCluster, "/clusters/") {
		t.Fatalf("gkeCluster not populated: %q", got.Config.GkeCluster)
	}

	if !strings.HasPrefix(got.Config.DagGcsPrefix, "gs://") || !strings.HasSuffix(got.Config.DagGcsPrefix, "/dags") {
		t.Fatalf("dagGcsPrefix not populated: %q", got.Config.DagGcsPrefix)
	}

	if !strings.Contains(got.Config.AirflowUri, location) {
		t.Fatalf("airflowUri not populated: %q", got.Config.AirflowUri)
	}

	if got.StorageConfig == nil || got.StorageConfig.Bucket == "" {
		t.Fatalf("storageConfig.bucket not populated: %+v", got.StorageConfig)
	}
}

// assertStableAcrossGets re-reads the environment and checks the computed
// outputs are identical, so a Terraform refresh sees no drift.
func assertStableAcrossGets(t *testing.T, svc *composer.Service, name string, first *composer.Environment) {
	t.Helper()

	second, err := svc.Projects.Locations.Environments.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Uuid != first.Uuid ||
		second.Config.GkeCluster != first.Config.GkeCluster ||
		second.Config.DagGcsPrefix != first.Config.DagGcsPrefix ||
		second.Config.AirflowUri != first.Config.AirflowUri ||
		second.CreateTime != first.CreateTime {
		t.Fatalf("computed outputs unstable across reads (drift): %+v vs %+v", first.Config, second.Config)
	}
}

func TestSDKComposerGetNotFound(t *testing.T) {
	svc, project := newSDKClient(t)

	_, err := svc.Projects.Locations.Environments.Get(
		"projects/" + project + "/locations/us-central1/environments/ghost").Do()
	if err == nil {
		t.Fatalf("expected NOT_FOUND error")
	}
}

func TestSDKComposerCreateDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/environments/dup"

	env := &composer.Environment{Name: name}

	if _, err := svc.Projects.Locations.Environments.Create(parent, env).Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Environments.Create(parent, env).Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
