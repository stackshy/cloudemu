package clouddns_test

import (
	"context"
	"testing"

	dns "google.golang.org/api/dns/v1"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/clouddns"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// TestSDKZonesScopedByProject verifies that Cloud DNS zone listing is isolated
// per project: a real dns.Service pointed at the emulator and listing under
// project A must return only A's zones, never zones created under project B.
// The handler records each zone's scope from the {project} path segment at
// create time and filters ManagedZones.List on it.
func TestSDKZonesScopedByProject(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	const projectA = "project-a"
	const projectB = "project-b"

	// The dns.Service is endpoint-pinned, not project-pinned: the project is a
	// per-call path segment, so two project paths exercise the isolation.
	for _, name := range []string{"a-one", "a-two"} {
		if _, err := svc.ManagedZones.Create(projectA, &dns.ManagedZone{
			Name:    name,
			DnsName: name + ".a.example.com.",
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Create(%s/%s): %v", projectA, name, err)
		}
	}

	for _, name := range []string{"b-one", "b-two", "b-three"} {
		if _, err := svc.ManagedZones.Create(projectB, &dns.ManagedZone{
			Name:    name,
			DnsName: name + ".b.example.com.",
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Create(%s/%s): %v", projectB, name, err)
		}
	}

	listNames := func(project string) []string {
		var names []string

		call := svc.ManagedZones.List(project).Context(ctx)
		if err := call.Pages(ctx, func(page *dns.ManagedZonesListResponse) error {
			for _, z := range page.ManagedZones {
				names = append(names, z.Name)
			}
			return nil
		}); err != nil {
			t.Fatalf("List(%s): %v", project, err)
		}

		return names
	}

	gotA := listNames(projectA)
	if len(gotA) != 2 {
		t.Fatalf("project A list = %v, want exactly its 2 zones", gotA)
	}
	for _, n := range gotA {
		if n != "a-one" && n != "a-two" {
			t.Fatalf("project A list leaked zone %q from another project: %v", n, gotA)
		}
	}

	gotB := listNames(projectB)
	if len(gotB) != 3 {
		t.Fatalf("project B list = %v, want exactly its 3 zones", gotB)
	}
	for _, n := range gotB {
		if n != "b-one" && n != "b-two" && n != "b-three" {
			t.Fatalf("project B list leaked zone %q from another project: %v", n, gotB)
		}
	}
}

// TestUpdateZoneProviderLevel exercises the driver's UpdateZone directly, since
// Cloud DNS zone create is not an upsert and the wire has no update path. It
// must match an existing zone by name, apply tags and scope, and return the
// updated copy — NotFound when the zone is absent.
func TestUpdateZoneProviderLevel(t *testing.T) {
	ctx := context.Background()

	opts := config.NewOptions(config.WithProjectID("test-project"))
	m := clouddns.New(opts)

	if _, err := m.CreateZone(ctx, dnsdriver.ZoneConfig{
		Name: "up.example.com",
		Tags: map[string]string{"env": "old"},
	}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	updated, err := m.UpdateZone(ctx, dnsdriver.ZoneConfig{
		Name:  "up.example.com",
		Tags:  map[string]string{"env": "new", "team": "dns"},
		Scope: scope.Scope{Project: "project-x"},
	})
	if err != nil {
		t.Fatalf("UpdateZone: %v", err)
	}

	if updated.Tags["env"] != "new" || updated.Tags["team"] != "dns" {
		t.Fatalf("tags not applied: %v", updated.Tags)
	}
	if updated.Scope.Project != "project-x" {
		t.Fatalf("scope not applied: %+v", updated.Scope)
	}

	got, err := m.GetZone(ctx, updated.ID)
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if got.Tags["env"] != "new" || got.Scope.Project != "project-x" {
		t.Fatalf("update not persisted: tags=%v scope=%+v", got.Tags, got.Scope)
	}

	if _, err := m.UpdateZone(ctx, dnsdriver.ZoneConfig{Name: "missing.example.com"}); err == nil {
		t.Fatal("UpdateZone(missing): expected NotFound error, got nil")
	}
}
