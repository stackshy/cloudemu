package vpcaccess_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/option"
	vpcaccess "google.golang.org/api/vpcaccess/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*vpcaccess.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := vpcaccess.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("vpcaccess.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKConnectorLifecycle drives the full connector lifecycle through the real
// google.golang.org/api/vpcaccess client: create (LRO), poll, get, list, patch,
// delete. The create leaves min/max instances+throughput UNSET, and the test
// asserts the emulator fills the real API defaults and reports them stably — the
// exact behavior a Terraform plan needs to converge (those fields are computed).
func TestSDKConnectorLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/connectors/conn"

	// A connector using the network + ip_cidr_range form (the network/subnet
	// oneof), leaving the numeric autoscaling fields unset.
	want := &vpcaccess.Connector{
		Network:     "default",
		IpCidrRange: "10.8.0.0/28",
	}

	op, err := svc.Projects.Locations.Connectors.Create(parent, want).
		ConnectorId("conn").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Connectors.Create: %v", err)
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

	got, err := svc.Projects.Locations.Connectors.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Connectors.Get: %v", err)
	}

	if got.Name != name {
		t.Fatalf("name = %q, want %q", got.Name, name)
	}

	if got.Network != "default" || got.IpCidrRange != "10.8.0.0/28" {
		t.Fatalf("network/ipCidrRange not round-tripped: %+v", got)
	}

	// Output-only state seeded READY so Terraform reconciles clean.
	if got.State != "READY" {
		t.Fatalf("state = %q, want READY", got.State)
	}

	// The classic drift point: computed numeric fields the client omitted must
	// come back filled with the real API defaults, identically on every read.
	if got.MinInstances != 2 || got.MaxInstances != 3 {
		t.Fatalf("instance defaults not filled: min=%d max=%d (want 2/3)", got.MinInstances, got.MaxInstances)
	}

	if got.MinThroughput != 200 || got.MaxThroughput != 300 {
		t.Fatalf("throughput defaults not filled: min=%d max=%d (want 200/300)", got.MinThroughput, got.MaxThroughput)
	}

	if got.MachineType != "e2-micro" {
		t.Fatalf("machineType default not filled: %q", got.MachineType)
	}

	// Stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Connectors.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.MinInstances != got.MinInstances || second.MaxInstances != got.MaxInstances ||
		second.MinThroughput != got.MinThroughput || second.MaxThroughput != got.MaxThroughput ||
		second.State != got.State {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// List.
	list, err := svc.Projects.Locations.Connectors.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Connectors) != 1 || !strings.HasSuffix(list.Connectors[0].Name, "/conn") {
		t.Fatalf("list = %+v", list.Connectors)
	}

	// Patch maxInstances via updateMask -> LRO, applied; network untouched.
	patchOp, err := svc.Projects.Locations.Connectors.Patch(name, &vpcaccess.Connector{
		MaxInstances: 5,
	}).UpdateMask("maxInstances").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Connectors.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Connectors.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.MaxInstances != 5 {
		t.Fatalf("maxInstances after patch = %d, want 5", updated.MaxInstances)
	}

	if updated.Network != "default" {
		t.Fatalf("network mutated by masked patch: %q", updated.Network)
	}

	delOp, err := svc.Projects.Locations.Connectors.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Connectors.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Connectors.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKConnectorSubnetForm verifies the subnet form of the network/subnet
// oneof round-trips (a connector houses in an existing subnet instead of
// network + ip_cidr_range).
func TestSDKConnectorSubnetForm(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/connectors/sub"

	want := &vpcaccess.Connector{
		Subnet: &vpcaccess.Subnet{Name: "conn-subnet", ProjectId: project},
	}

	if _, err := svc.Projects.Locations.Connectors.Create(parent, want).ConnectorId("sub").Do(); err != nil {
		t.Fatalf("Create (subnet form): %v", err)
	}

	got, err := svc.Projects.Locations.Connectors.Get(name).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Subnet == nil || got.Subnet.Name != "conn-subnet" || got.Subnet.ProjectId != project {
		t.Fatalf("subnet not round-tripped: %+v", got.Subnet)
	}

	// A subnet-form connector still fills the computed autoscaling defaults.
	if got.MinInstances != 2 || got.MaxInstances != 3 {
		t.Fatalf("instance defaults not filled on subnet form: %+v", got)
	}
}

func TestSDKConnectorNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Connectors.Get(parent + "/connectors/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	c := &vpcaccess.Connector{Network: "default", IpCidrRange: "10.8.0.0/28"}

	if _, err := svc.Projects.Locations.Connectors.Create(parent, c).ConnectorId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Connectors.Create(parent, c).ConnectorId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
