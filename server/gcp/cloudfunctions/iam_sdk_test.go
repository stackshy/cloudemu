package cloudfunctions_test

import (
	"context"
	"testing"

	"google.golang.org/api/cloudfunctions/v1"
)

// TestSDKCloudFunctionsSetGetIamPolicy exercises the invoker-policy round-trip
// that Terraform's google_cloudfunctions_function_iam_member performs to make an
// HTTP function public: setIamPolicy granting roles/cloudfunctions.invoker to
// allUsers, then getIamPolicy to read it back.
func TestSDKCloudFunctionsSetGetIamPolicy(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"
	name := parent + "/functions/hello"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:    name,
		Runtime: "go121",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A function with no policy yet returns an empty, versioned policy.
	empty, err := svc.Projects.Locations.Functions.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (initial): %v", err)
	}

	if len(empty.Bindings) != 0 {
		t.Fatalf("initial policy has %d bindings, want 0", len(empty.Bindings))
	}

	// Grant the invoker role to allUsers.
	set, err := svc.Projects.Locations.Functions.SetIamPolicy(name, &cloudfunctions.SetIamPolicyRequest{
		Policy: &cloudfunctions.Policy{
			Bindings: []*cloudfunctions.Binding{{
				Role:    "roles/cloudfunctions.invoker",
				Members: []string{"allUsers"},
			}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Bindings[0].Role != "roles/cloudfunctions.invoker" {
		t.Fatalf("SetIamPolicy returned unexpected bindings: %+v", set.Bindings)
	}

	// getIamPolicy must round-trip the binding.
	got, err := svc.Projects.Locations.Functions.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 {
		t.Fatalf("policy has %d bindings, want 1", len(got.Bindings))
	}

	if got.Bindings[0].Role != "roles/cloudfunctions.invoker" {
		t.Fatalf("role = %q, want roles/cloudfunctions.invoker", got.Bindings[0].Role)
	}

	if len(got.Bindings[0].Members) != 1 || got.Bindings[0].Members[0] != "allUsers" {
		t.Fatalf("members = %v, want [allUsers]", got.Bindings[0].Members)
	}
}

// TestSDKCloudFunctionsGetIamPolicyMissingFunction confirms getIamPolicy on a
// non-existent function surfaces an error rather than an empty policy.
func TestSDKCloudFunctionsGetIamPolicyMissingFunction(t *testing.T) {
	svc := newGCPSDKService(t)

	name := "projects/demo/locations/us-central1/functions/ghost"
	if _, err := svc.Projects.Locations.Functions.GetIamPolicy(name).Context(context.Background()).Do(); err == nil {
		t.Fatal("GetIamPolicy on missing function returned nil error, want NotFound")
	}
}
