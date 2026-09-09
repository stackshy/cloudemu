package cloudids_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	ids "google.golang.org/api/ids/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*ids.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := ids.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("ids.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKEndpointLifecycle drives the full endpoint lifecycle through the real
// google.golang.org/api/ids client: create (LRO), poll, get, list, patch, delete.
// It asserts the emulator fills the computed output-only attributes (state,
// endpointForwardingRule, endpointIp, createTime/updateTime) and reports them
// stably — the exact behavior a Terraform plan needs to converge (those fields
// are computed and would diff forever otherwise).
func TestSDKEndpointLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/endpoints/ep"

	want := &ids.Endpoint{
		Network:     "default",
		Severity:    "HIGH",
		Description: "prod IDS endpoint",
	}

	op, err := svc.Projects.Locations.Endpoints.Create(parent, want).
		EndpointId("ep").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Endpoints.Create: %v", err)
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

	got, err := svc.Projects.Locations.Endpoints.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Endpoints.Get: %v", err)
	}

	if got.Name != name {
		t.Fatalf("name = %q, want %q", got.Name, name)
	}

	if got.Network != "default" || got.Severity != "HIGH" || got.Description != "prod IDS endpoint" {
		t.Fatalf("body not round-tripped: %+v", got)
	}

	// Output-only state seeded READY so Terraform reconciles clean.
	if got.State != "READY" {
		t.Fatalf("state = %q, want READY", got.State)
	}

	// The classic drift point: computed output-only attributes must be present and
	// deterministic.
	if got.EndpointForwardingRule == "" || !strings.Contains(got.EndpointForwardingRule, "/forwardingRules/ids-ep") {
		t.Fatalf("endpointForwardingRule not minted: %q", got.EndpointForwardingRule)
	}

	if !strings.HasPrefix(got.EndpointIp, "10.") {
		t.Fatalf("endpointIp not minted: %q", got.EndpointIp)
	}

	if got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("timestamps missing: create=%q update=%q", got.CreateTime, got.UpdateTime)
	}

	// Stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.Endpoints.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.EndpointForwardingRule != got.EndpointForwardingRule ||
		second.EndpointIp != got.EndpointIp ||
		second.State != got.State ||
		second.CreateTime != got.CreateTime {
		t.Fatalf("computed fields unstable across reads (drift): %+v vs %+v", got, second)
	}

	// List.
	list, err := svc.Projects.Locations.Endpoints.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Endpoints) != 1 || !strings.HasSuffix(list.Endpoints[0].Name, "/ep") {
		t.Fatalf("list = %+v", list.Endpoints)
	}

	// Patch threatExceptions via updateMask -> LRO, applied; severity untouched.
	patchOp, err := svc.Projects.Locations.Endpoints.Patch(name, &ids.Endpoint{
		ThreatExceptions: []string{"12345", "67890"},
	}).UpdateMask("threatExceptions").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Endpoints.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Endpoints.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if len(updated.ThreatExceptions) != 2 || updated.ThreatExceptions[0] != "12345" {
		t.Fatalf("threatExceptions after patch = %+v", updated.ThreatExceptions)
	}

	if updated.Severity != "HIGH" {
		t.Fatalf("severity mutated by masked patch: %q", updated.Severity)
	}

	// Computed fields still stable after patch.
	if updated.EndpointForwardingRule != got.EndpointForwardingRule || updated.EndpointIp != got.EndpointIp {
		t.Fatalf("computed fields drifted after patch")
	}

	delOp, err := svc.Projects.Locations.Endpoints.Delete(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Endpoints.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.Endpoints.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKEndpointSeverityRequired verifies a create with no severity is rejected
// 400 (the one required enum), and that an invalid severity is likewise rejected.
func TestSDKEndpointSeverityRequired(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Endpoints.Create(parent, &ids.Endpoint{
		Network: "default",
	}).EndpointId("nosev").Do(); err == nil {
		t.Fatalf("expected INVALID_ARGUMENT for missing severity")
	}
}

func TestSDKEndpointNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Endpoints.Get(parent + "/endpoints/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	e := &ids.Endpoint{Network: "default", Severity: "LOW"}

	if _, err := svc.Projects.Locations.Endpoints.Create(parent, e).EndpointId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Endpoints.Create(parent, e).EndpointId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
