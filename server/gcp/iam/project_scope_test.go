package iam_test

import (
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"
)

func roleIsStatus(err error, code int) bool {
	var gerr *googleapi.Error

	return errors.As(err, &gerr) && gerr.Code == code
}

func createProjectRole(t *testing.T, svc *iamv1.Service, project, id, perm string) {
	t.Helper()

	if _, err := svc.Projects.Roles.Create("projects/"+project, &iamv1.CreateRoleRequest{
		RoleId: id,
		Role:   &iamv1.Role{Title: project, IncludedPermissions: []string{perm}, Stage: "GA"},
	}).Do(); err != nil {
		t.Fatalf("create projects/%s/roles/%s: %v", project, id, err)
	}
}

// TestRoleSameIDTwoProjects pins GIAM-06: a custom role id is unique per
// parent project, so the same id in two projects is two roles.
func TestRoleSameIDTwoProjects(t *testing.T) {
	svc := newSDKService(t)

	createProjectRole(t, svc, "p-a", "w2Role", "pubsub.topics.get")
	createProjectRole(t, svc, "p-b", "w2Role", "storage.buckets.get")

	for project, perm := range map[string]string{"p-a": "pubsub.topics.get", "p-b": "storage.buckets.get"} {
		got, err := svc.Projects.Roles.Get("projects/" + project + "/roles/w2Role").Do()
		if err != nil {
			t.Fatalf("get %s: %v", project, err)
		}

		if len(got.IncludedPermissions) != 1 || got.IncludedPermissions[0] != perm || got.Title != project {
			t.Fatalf("%s role = %+v, want its own permission %s", project, got, perm)
		}
	}

	if _, err := svc.Projects.Roles.Get("projects/p-c/roles/w2Role").Do(); !roleIsStatus(err, http.StatusNotFound) {
		t.Fatalf("get under p-c: err = %v, want 404", err)
	}

	list, err := svc.Projects.Roles.List("projects/p-b").Do()
	if err != nil {
		t.Fatalf("list p-b: %v", err)
	}

	if len(list.Roles) != 1 || list.Roles[0].IncludedPermissions[0] != "storage.buckets.get" {
		t.Fatalf("p-b roles = %+v", list.Roles)
	}
}

// TestRoleDeleteUndeletePerProject pins that delete and undelete act on one
// project's role and leave the same id elsewhere untouched.
func TestRoleDeleteUndeletePerProject(t *testing.T) {
	svc := newSDKService(t)

	createProjectRole(t, svc, "p-a", "r", "a.perm")
	createProjectRole(t, svc, "p-b", "r", "b.perm")

	if _, err := svc.Projects.Roles.Delete("projects/p-b/roles/r").Do(); err != nil {
		t.Fatalf("delete p-b: %v", err)
	}

	if _, err := svc.Projects.Roles.Get("projects/p-a/roles/r").Do(); err != nil {
		t.Fatalf("p-a role after p-b delete: %v", err)
	}

	if _, err := svc.Projects.Roles.Delete("projects/p-a/roles/r").Do(); err != nil {
		t.Fatalf("delete p-a: %v", err)
	}

	got, err := svc.Projects.Roles.Undelete("projects/p-b/roles/r", &iamv1.UndeleteRoleRequest{}).Do()
	if err != nil {
		t.Fatalf("undelete p-b: %v", err)
	}

	if got.IncludedPermissions[0] != "b.perm" {
		t.Fatalf("undeleted p-b role = %+v, want b.perm", got)
	}

	if _, err := svc.Projects.Roles.Get("projects/p-a/roles/r").Do(); !roleIsStatus(err, http.StatusNotFound) {
		t.Fatalf("p-a role after p-b undelete: err = %v, want 404", err)
	}
}

// TestRoleBindingResolvesInItsProject pins that a binding on
// projects/p-b/roles/r grants p-b's permissions, not p-a's same-id role.
func TestRoleBindingResolvesInItsProject(t *testing.T) {
	svc := newSDKService(t)

	createProjectRole(t, svc, "p-a", "r", "a.perm")
	createProjectRole(t, svc, "p-b", "r", "b.perm")

	sa, err := svc.Projects.ServiceAccounts.Create("projects/p-a", &iamv1.CreateServiceAccountRequest{
		AccountId: "binder",
	}).Do()
	if err != nil {
		t.Fatalf("create SA: %v", err)
	}

	resource := "projects/p-a/serviceAccounts/" + sa.Email
	if _, err := svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{Bindings: []*iamv1.Binding{{
			Role: "projects/p-b/roles/r", Members: []string{"user:x@example.com"},
		}}},
	}).Do(); err != nil {
		t.Fatalf("setIamPolicy: %v", err)
	}

	resp, err := svc.Projects.ServiceAccounts.TestIamPermissions(resource, &iamv1.TestIamPermissionsRequest{
		Permissions: []string{"a.perm", "b.perm"},
	}).Do()
	if err != nil {
		t.Fatalf("testIamPermissions: %v", err)
	}

	if len(resp.Permissions) != 1 || resp.Permissions[0] != "b.perm" {
		t.Fatalf("held = %v, want [b.perm]", resp.Permissions)
	}
}
