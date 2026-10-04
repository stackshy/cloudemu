package resourcemanager_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const testProject = "demo-project"

func newCRMService(t *testing.T) *crm.Service {
	t.Helper()

	// The project-IAM handler has no driver, so an empty Drivers bundle still
	// registers it (like cloudbilling/servicenetworking).
	srv := gcpserver.New(gcpserver.Drivers{})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := crm.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("crm.NewService: %v", err)
	}

	return svc
}

func TestSDKProjectIamPolicyRoundTrip(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	// getIamPolicy on a project with no policy set returns a versioned, etagged
	// policy (real GCP never 404s getIamPolicy on an existing resource).
	pol, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy(empty): %v", err)
	}

	if pol.Etag == "" {
		t.Fatal("empty-policy get returned no etag")
	}

	// setIamPolicy with the fetched etag is accepted and echoes the bindings.
	set, err := svc.Projects.SetIamPolicy(testProject, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{
			Bindings: []*crm.Binding{{
				Role:    "roles/viewer",
				Members: []string{"user:alice@example.com"},
			}},
			Etag: pol.Etag,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Bindings[0].Role != "roles/viewer" {
		t.Fatalf("set policy bindings mismatch: %+v", set.Bindings)
	}

	if set.Etag == pol.Etag {
		t.Fatal("etag did not change after a write")
	}

	// getIamPolicy round-trips the stored bindings and the new etag.
	got, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy(after set): %v", err)
	}

	if got.Etag != set.Etag {
		t.Fatalf("etag drift: get %q != set %q", got.Etag, set.Etag)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Members[0] != "user:alice@example.com" {
		t.Fatalf("round-trip bindings mismatch: %+v", got.Bindings)
	}
}

func TestSDKProjectIamPolicyStaleEtagConflict(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	first, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if _, err = svc.Projects.SetIamPolicy(testProject, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{
			Bindings: []*crm.Binding{{Role: "roles/editor", Members: []string{"user:bob@example.com"}}},
			Etag:     first.Etag,
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("first SetIamPolicy: %v", err)
	}

	// Reusing the now-stale etag must be rejected with 409 ABORTED, the
	// read-modify-write contract Terraform's google_project_iam_* rely on.
	_, err = svc.Projects.SetIamPolicy(testProject, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{
			Bindings: []*crm.Binding{{Role: "roles/owner", Members: []string{"user:carol@example.com"}}},
			Etag:     first.Etag, // stale
		},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 409 {
		t.Fatalf("stale-etag set: want 409 conflict, got %v", err)
	}
}

func TestSDKProjectIamPolicyConditionAndAuditRoundTrip(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	pol, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	_, err = svc.Projects.SetIamPolicy(testProject, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{
			Version: 3,
			Bindings: []*crm.Binding{{
				Role:    "roles/viewer",
				Members: []string{"user:alice@example.com"},
				Condition: &crm.Expr{
					Title:      "expires",
					Expression: `request.time < timestamp("2030-01-01T00:00:00Z")`,
				},
			}},
			AuditConfigs: []*crm.AuditConfig{{
				Service:         "allServices",
				AuditLogConfigs: []*crm.AuditLogConfig{{LogType: "DATA_READ"}},
			}},
			Etag: pol.Etag,
		},
		// Without auditConfigs in the mask, real IAM keeps the stored audit
		// configs (the default mask is "bindings,etag"). Terraform's
		// google_project_iam_audit_config sends this mask.
		UpdateMask: "bindings,etag,auditConfigs",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy(after set): %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Condition == nil ||
		got.Bindings[0].Condition.Title != "expires" {
		t.Fatalf("binding condition did not round-trip: %+v", got.Bindings)
	}

	if len(got.AuditConfigs) != 1 || got.AuditConfigs[0].Service != "allServices" ||
		len(got.AuditConfigs[0].AuditLogConfigs) != 1 ||
		got.AuditConfigs[0].AuditLogConfigs[0].LogType != "DATA_READ" {
		t.Fatalf("audit config did not round-trip: %+v", got.AuditConfigs)
	}
}

func TestSDKProjectTestIamPermissions(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	want := []string{"resourcemanager.projects.get", "resourcemanager.projects.setIamPolicy"}

	resp, err := svc.Projects.TestIamPermissions(testProject, &crm.TestIamPermissionsRequest{
		Permissions: want,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(resp.Permissions) != len(want) {
		t.Fatalf("got %v, want %v", resp.Permissions, want)
	}
}

// TestSDKProjectIamPolicyBlindWrite: a setIamPolicy without an etag overwrites
// unconditionally, even when a policy is already stored (T4-14).
func TestSDKProjectIamPolicyBlindWrite(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	for _, member := range []string{"user:a@example.com", "user:b@example.com"} {
		_, err := svc.Projects.SetIamPolicy(testProject, &crm.SetIamPolicyRequest{
			Policy: &crm.Policy{Bindings: []*crm.Binding{{Role: "roles/viewer", Members: []string{member}}}},
		}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("blind SetIamPolicy(%s): %v", member, err)
		}
	}

	got, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Members[0] != "user:b@example.com" {
		t.Fatalf("blind write did not overwrite: %+v", got.Bindings)
	}
}

// TestSDKOrganizationIamPolicy: organizations/{o} serves the same IAM verbs
// as projects, with a policy separate from any project's.
func TestSDKOrganizationIamPolicy(t *testing.T) {
	svc := newCRMService(t)
	ctx := context.Background()

	const org = "organizations/123456"

	pol, err := svc.Organizations.GetIamPolicy(org, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	set, err := svc.Organizations.SetIamPolicy(org, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{
			Bindings: []*crm.Binding{{Role: "roles/browser", Members: []string{"user:o@example.com"}}},
			Etag:     pol.Etag,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	_, err = svc.Organizations.SetIamPolicy(org, &crm.SetIamPolicyRequest{
		Policy: &crm.Policy{Etag: pol.Etag},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 409 {
		t.Fatalf("stale-etag set: want 409, got %v", err)
	}

	got, err := svc.Organizations.GetIamPolicy(org, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil || got.Etag != set.Etag || len(got.Bindings) != 1 {
		t.Fatalf("GetIamPolicy after set = %+v, %v", got, err)
	}

	proj, err := svc.Projects.GetIamPolicy(testProject, &crm.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil || len(proj.Bindings) != 0 {
		t.Fatalf("org policy leaked into project: %+v, %v", proj, err)
	}

	perms, err := svc.Organizations.TestIamPermissions(org, &crm.TestIamPermissionsRequest{
		Permissions: []string{"resourcemanager.organizations.get"},
	}).Context(ctx).Do()
	if err != nil || len(perms.Permissions) != 1 {
		t.Fatalf("TestIamPermissions = %+v, %v", perms, err)
	}
}
