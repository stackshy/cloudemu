package privateca_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
	privateca "google.golang.org/api/privateca/v1"
)

// TestSDKCaPoolIamPolicy drives caPools getIamPolicy (a GET in the real API),
// setIamPolicy and testIamPermissions, the calls behind
// google_privateca_ca_pool_iam_*. A missing pool is a 404 and a recreated pool
// starts with an empty policy.
func TestSDKCaPoolIamPolicy(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	pool := parent + "/caPools/iam-pool"

	createPool := func() {
		t.Helper()

		if _, err := svc.Projects.Locations.CaPools.Create(parent, &privateca.CaPool{Tier: "DEVOPS"}).
			CaPoolId("iam-pool").Context(ctx).Do(); err != nil {
			t.Fatalf("CaPools.Create: %v", err)
		}
	}

	createPool()

	pol, err := svc.Projects.Locations.CaPools.GetIamPolicy(pool).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	set, err := svc.Projects.Locations.CaPools.SetIamPolicy(pool, &privateca.SetIamPolicyRequest{
		Policy: &privateca.Policy{
			Bindings: []*privateca.Binding{{Role: "roles/privateca.certificateRequester", Members: []string{"user:a@example.com"}}},
			Etag:     pol.Etag,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := svc.Projects.Locations.CaPools.GetIamPolicy(pool).Context(ctx).Do()
	if err != nil || got.Etag != set.Etag || len(got.Bindings) != 1 {
		t.Fatalf("GetIamPolicy after set = %+v, %v", got, err)
	}

	perms, err := svc.Projects.Locations.CaPools.TestIamPermissions(pool, &privateca.TestIamPermissionsRequest{
		Permissions: []string{"privateca.caPools.get"},
	}).Context(ctx).Do()
	if err != nil || len(perms.Permissions) != 1 {
		t.Fatalf("TestIamPermissions = %+v, %v", perms, err)
	}

	_, err = svc.Projects.Locations.CaPools.GetIamPolicy(parent + "/caPools/missing").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("missing pool: want 404, got %v", err)
	}

	if _, err = svc.Projects.Locations.CaPools.Delete(pool).Context(ctx).Do(); err != nil {
		t.Fatalf("CaPools.Delete: %v", err)
	}

	createPool()

	fresh, err := svc.Projects.Locations.CaPools.GetIamPolicy(pool).Context(ctx).Do()
	if err != nil || len(fresh.Bindings) != 0 {
		t.Fatalf("recreated pool policy = %+v, %v; want empty", fresh, err)
	}
}

// TestSDKCertificateTemplateIamPolicy covers google_privateca_certificate_template_iam_*.
func TestSDKCertificateTemplateIamPolicy(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	tmpl := parent + "/certificateTemplates/iam-tmpl"

	if _, err := svc.Projects.Locations.CertificateTemplates.Create(parent, &privateca.CertificateTemplate{}).
		CertificateTemplateId("iam-tmpl").Context(ctx).Do(); err != nil {
		t.Fatalf("CertificateTemplates.Create: %v", err)
	}

	_, err := svc.Projects.Locations.CertificateTemplates.SetIamPolicy(tmpl, &privateca.SetIamPolicyRequest{
		Policy: &privateca.Policy{
			Bindings: []*privateca.Binding{{Role: "roles/privateca.templateUser", Members: []string{"user:a@example.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := svc.Projects.Locations.CertificateTemplates.GetIamPolicy(tmpl).Context(ctx).Do()
	if err != nil || len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/privateca.templateUser" {
		t.Fatalf("GetIamPolicy = %+v, %v", got, err)
	}
}
