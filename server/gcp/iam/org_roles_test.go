package iam_test

import (
	"net/http"
	"testing"

	iamv1 "google.golang.org/api/iam/v1"
)

// TestOrgCustomRoleLifecycle pins GIAM-N2: organization custom roles are
// served (CRUD and undelete), live apart from a same-id project role, and a
// binding on organizations/{o}/roles/{id} grants that role's permissions.
func TestOrgCustomRoleLifecycle(t *testing.T) {
	svc := newSDKService(t)
	org := "organizations/1234"
	name := org + "/roles/orgRole"

	created, err := svc.Organizations.Roles.Create(org, &iamv1.CreateRoleRequest{
		RoleId: "orgRole",
		Role:   &iamv1.Role{Title: "org", IncludedPermissions: []string{"org.perm"}, Stage: "GA"},
	}).Do()
	if err != nil {
		t.Fatalf("create org role: %v", err)
	}

	if created.Name != name {
		t.Fatalf("created name = %q, want %q", created.Name, name)
	}

	createProjectRole(t, svc, "p-a", "orgRole", "project.perm")

	got, err := svc.Organizations.Roles.Get(name).Do()
	if err != nil || got.IncludedPermissions[0] != "org.perm" {
		t.Fatalf("get org role = %+v, %v", got, err)
	}

	if _, err := svc.Organizations.Roles.Patch(name, &iamv1.Role{Title: "renamed", IncludedPermissions: []string{"org.perm"}, Stage: "GA"}).Do(); err != nil {
		t.Fatalf("patch org role: %v", err)
	}

	list, err := svc.Organizations.Roles.List(org).Do()
	if err != nil || len(list.Roles) != 1 || list.Roles[0].Name != name || list.Roles[0].Title != "renamed" {
		t.Fatalf("list org roles = %+v, %v", list, err)
	}

	sa, err := svc.Projects.ServiceAccounts.Create("projects/p-a", &iamv1.CreateServiceAccountRequest{AccountId: "orgbinder"}).Do()
	if err != nil {
		t.Fatalf("create SA: %v", err)
	}

	resource := "projects/p-a/serviceAccounts/" + sa.Email
	if _, err := svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{Bindings: []*iamv1.Binding{{Role: name, Members: []string{"user:x@example.com"}}}},
	}).Do(); err != nil {
		t.Fatalf("setIamPolicy: %v", err)
	}

	held, err := svc.Projects.ServiceAccounts.TestIamPermissions(resource, &iamv1.TestIamPermissionsRequest{
		Permissions: []string{"org.perm", "project.perm"},
	}).Do()
	if err != nil || len(held.Permissions) != 1 || held.Permissions[0] != "org.perm" {
		t.Fatalf("held = %+v, %v; want [org.perm]", held, err)
	}

	if _, err := svc.Organizations.Roles.Delete(name).Do(); err != nil {
		t.Fatalf("delete org role: %v", err)
	}

	if _, err := svc.Organizations.Roles.Get(name).Do(); !roleIsStatus(err, http.StatusNotFound) {
		t.Fatalf("get after delete: err = %v, want 404", err)
	}

	if _, err := svc.Projects.Roles.Get("projects/p-a/roles/orgRole").Do(); err != nil {
		t.Fatalf("project role after org delete: %v", err)
	}

	if _, err := svc.Organizations.Roles.Undelete(name, &iamv1.UndeleteRoleRequest{}).Do(); err != nil {
		t.Fatalf("undelete org role: %v", err)
	}
}
