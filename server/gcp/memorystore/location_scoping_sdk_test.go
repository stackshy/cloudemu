package memorystore_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
	redis "google.golang.org/api/redis/v1"
)

// TestSDKLocationScoping locks the per-location semantics of the real
// Memorystore API: an instance id is unique per (project, location), so Get in
// the wrong location is NOT_FOUND, List is scoped to its parent's region, and the
// "-" location wildcard aggregates every region with each instance reported under
// its actual location (never the wildcard).
func TestSDKLocationScoping(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	const project = "demo"

	loc := func(region string) string { return "projects/" + project + "/locations/" + region }
	name := func(region, id string) string { return loc(region) + "/instances/" + id }

	// Two instances with the same short id in different regions must coexist? The
	// shared store keys on the id, so use distinct ids per region to model a
	// realistic multi-region deployment.
	create := func(region, id string) {
		t.Helper()

		if _, err := svc.Projects.Locations.Instances.Create(loc(region), &redis.Instance{
			Tier:         "BASIC",
			MemorySizeGb: 1,
		}).InstanceId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create(%s/%s): %v", region, id, err)
		}
	}

	create("us-central1", "central-cache")
	create("us-east1", "east-cache")

	// Get in the correct location succeeds and reports the true resource name.
	got, err := svc.Projects.Locations.Instances.Get(name("us-central1", "central-cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(us-central1/central-cache): %v", err)
	}

	if got.Name != name("us-central1", "central-cache") {
		t.Errorf("name: got %q want %q", got.Name, name("us-central1", "central-cache"))
	}

	if got.LocationId != "us-central1-a" {
		t.Errorf("locationId: got %q want us-central1-a", got.LocationId)
	}

	// Get with the WRONG location for an existing id is NOT_FOUND (not a phantom
	// resource named after the request path).
	_, err = svc.Projects.Locations.Instances.Get(name("us-east1", "central-cache")).Context(ctx).Do()
	assertNotFound(t, err, "Get(wrong location)")

	// List is scoped to its parent region.
	assertListNames(t, svc, loc("us-central1"), []string{name("us-central1", "central-cache")})
	assertListNames(t, svc, loc("us-east1"), []string{name("us-east1", "east-cache")})
	assertListNames(t, svc, loc("europe-west1"), nil)

	// The "-" wildcard aggregates all regions, each under its actual location.
	assertListNames(t, svc, loc("-"), []string{
		name("us-central1", "central-cache"),
		name("us-east1", "east-cache"),
	})

	// Patch/Delete in the wrong location are NOT_FOUND.
	_, err = svc.Projects.Locations.Instances.Patch(name("us-east1", "central-cache"),
		&redis.Instance{MemorySizeGb: 2}).UpdateMask("memorySizeGb").Context(ctx).Do()
	assertNotFound(t, err, "Patch(wrong location)")

	_, err = svc.Projects.Locations.Instances.Delete(name("us-east1", "central-cache")).Context(ctx).Do()
	assertNotFound(t, err, "Delete(wrong location)")

	// The instance is untouched by the wrong-location mutations.
	after, err := svc.Projects.Locations.Instances.Get(name("us-central1", "central-cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after wrong-location mutations: %v", err)
	}

	if after.MemorySizeGb != 1 {
		t.Errorf("memorySizeGb changed by wrong-location patch: got %d want 1", after.MemorySizeGb)
	}
}

func assertNotFound(t *testing.T, err error, op string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected NOT_FOUND, got nil", op)
	}

	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code != 404 {
		t.Fatalf("%s: expected HTTP 404, got %v", op, err)
	}
}

func assertListNames(t *testing.T, svc *redis.Service, parent string, want []string) {
	t.Helper()

	ctx := context.Background()

	var got []string

	call := svc.Projects.Locations.Instances.List(parent).Context(ctx)
	if err := call.Pages(ctx, func(page *redis.ListInstancesResponse) error {
		for _, in := range page.Instances {
			got = append(got, in.Name)
		}

		return nil
	}); err != nil {
		t.Fatalf("List(%s): %v", parent, err)
	}

	if len(got) != len(want) {
		t.Fatalf("List(%s): got %d names %v, want %d %v", parent, len(got), got, len(want), want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List(%s)[%d]: got %q want %q (full %v)", parent, i, got[i], want[i], got)
		}
	}
}
