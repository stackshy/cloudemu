package datafusion_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	datafusion "google.golang.org/api/datafusion/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*datafusion.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := datafusion.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("datafusion.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKInstanceLifecycle exercises the full instance surface: a create with
// type/version/networkConfig/labels; the LRO settling done with the instance in
// its typed response; the computed identity + create-derived output fields;
// their byte-stability across reads (the Terraform drift point); a masked patch
// of labels; and delete → 404.
func TestSDKInstanceLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/instances/df"

	op, err := svc.Projects.Locations.Instances.Create(parent, &datafusion.Instance{
		Type:        "ENTERPRISE",
		Version:     "6.9.2",
		DisplayName: "Analytics",
		Description: "etl",
		Labels:      map[string]string{"team": "data"},
		NetworkConfig: &datafusion.NetworkConfig{
			Network:      "default",
			IpAllocation: "10.89.0.0/22",
		},
	}).InstanceId("df").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Create: %v", err)
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

	got, err := svc.Projects.Locations.Instances.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if got.Name != name || got.State != "ACTIVE" || got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed identity wrong: %+v", got)
	}

	if got.ServiceEndpoint == "" || got.ApiEndpoint == "" || got.GcsBucket == "" ||
		got.TenantProjectId == "" || got.P4ServiceAccount == "" || got.ServiceAccount == "" {
		t.Fatalf("create-derived output fields missing: %+v", got)
	}

	if got.Type != "ENTERPRISE" || got.Version != "6.9.2" || got.Description != "etl" ||
		got.Labels["team"] != "data" || got.NetworkConfig == nil || got.NetworkConfig.IpAllocation != "10.89.0.0/22" {
		t.Fatalf("inputs not round-tripped: %+v", got)
	}

	// Byte-stable across reads (no Terraform drift on the computed output fields).
	second, err := svc.Projects.Locations.Instances.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.ServiceEndpoint != got.ServiceEndpoint || second.ApiEndpoint != got.ApiEndpoint ||
		second.GcsBucket != got.GcsBucket || second.TenantProjectId != got.TenantProjectId ||
		second.P4ServiceAccount != got.P4ServiceAccount || second.ServiceAccount != got.ServiceAccount ||
		second.Version != got.Version || second.CreateTime != got.CreateTime ||
		second.UpdateTime != got.UpdateTime || second.State != got.State {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	patchOp, err := svc.Projects.Locations.Instances.Patch(name, &datafusion.Instance{
		Labels: map[string]string{"team": "data", "env": "prod"},
	}).UpdateMask("labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Instances.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Labels["env"] != "prod" || updated.Type != "ENTERPRISE" ||
		updated.NetworkConfig == nil || updated.NetworkConfig.IpAllocation != "10.89.0.0/22" {
		t.Fatalf("masked patch wrong (mutated unmentioned fields or dropped labels): %+v", updated)
	}

	delOp, err := svc.Projects.Locations.Instances.Delete(name).Do()
	if err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Instances.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKRestart drives the :restart custom verb: a restart of an ACTIVE instance
// succeeds and leaves it ACTIVE; a restart of a missing instance 404s.
func TestSDKRestart(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/instances/df"

	if _, err := svc.Projects.Locations.Instances.Create(parent, &datafusion.Instance{
		Type: "BASIC",
	}).InstanceId("df").Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	op, err := svc.Projects.Locations.Instances.Restart(name, &datafusion.RestartInstanceRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Restart: %v", err)
	}

	if !op.Done {
		t.Fatalf("restart operation not done")
	}

	got, err := svc.Projects.Locations.Instances.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}

	if got.State != "ACTIVE" {
		t.Fatalf("state after restart = %q, want ACTIVE", got.State)
	}

	if _, err := svc.Projects.Locations.Instances.Restart(
		parent+"/instances/ghost", &datafusion.RestartInstanceRequest{}).Do(); err == nil {
		t.Fatalf("expected 404 restarting a missing instance")
	}
}

// TestSDKListAndDuplicate covers list scoping and the ALREADY_EXISTS duplicate
// create.
func TestSDKListAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	inst := &datafusion.Instance{Type: "BASIC"}

	if _, err := svc.Projects.Locations.Instances.Create(parent, inst).InstanceId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Instances.Create(parent, inst).InstanceId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}

	list, err := svc.Projects.Locations.Instances.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Instances) != 1 || !strings.HasSuffix(list.Instances[0].Name, "/dup") {
		t.Fatalf("list = %+v", list.Instances)
	}
}
