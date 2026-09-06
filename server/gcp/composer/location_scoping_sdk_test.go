package composer_test

import (
	"context"
	"testing"

	composer "google.golang.org/api/composer/v1"
)

// TestSDKComposerLocationScoping verifies an environment created in one location
// is not resolvable under a different location or project — environment names
// are unique per (project, location), so a cross-location Get/Delete 404s.
func TestSDKComposerLocationScoping(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	parent := "projects/" + project + "/locations/us-central1"
	name := parent + "/environments/scoped"

	if _, err := svc.Projects.Locations.Environments.Create(parent, &composer.Environment{
		Name: name,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Same id, different location -> NOT_FOUND.
	otherLoc := "projects/" + project + "/locations/europe-west1/environments/scoped"
	if _, err := svc.Projects.Locations.Environments.Get(otherLoc).Context(ctx).Do(); err == nil {
		t.Fatalf("expected NOT_FOUND for env in a different location")
	}

	// Same id, different project -> NOT_FOUND.
	otherProj := "projects/other-project/locations/us-central1/environments/scoped"
	if _, err := svc.Projects.Locations.Environments.Get(otherProj).Context(ctx).Do(); err == nil {
		t.Fatalf("expected NOT_FOUND for env in a different project")
	}

	// Correct scope resolves.
	if _, err := svc.Projects.Locations.Environments.Get(name).Context(ctx).Do(); err != nil {
		t.Fatalf("get in correct scope: %v", err)
	}

	// List in the other location is empty.
	list, err := svc.Projects.Locations.Environments.List(
		"projects/" + project + "/locations/europe-west1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("list other location: %v", err)
	}

	if len(list.Environments) != 0 {
		t.Fatalf("expected no environments in europe-west1, got %d", len(list.Environments))
	}
}
