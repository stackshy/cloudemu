package iam_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

const (
	testProject = "demo-project"
)

func newSDKService(t *testing.T) *iamv1.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{IAM: cloud.IAM})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := iamv1.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("iamv1.NewService: %v", err)
	}

	return svc
}

func TestSDKGCPIAMServiceAccountLifecycle(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	created, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "ci-deployer",
		ServiceAccount: &iamv1.ServiceAccount{
			DisplayName: "CI Deployer",
			Description: "Used by CI to deploy",
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantEmail := "ci-deployer@" + testProject + ".iam.gserviceaccount.com"
	if created.Email != wantEmail {
		t.Fatalf("got email %q, want %q", created.Email, wantEmail)
	}

	if created.DisplayName != "CI Deployer" {
		t.Fatalf("got displayName %q, want CI Deployer", created.DisplayName)
	}

	got, err := svc.Projects.ServiceAccounts.Get(
		"projects/-/serviceAccounts/" + wantEmail,
	).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Email != wantEmail {
		t.Fatalf("Get returned email %q, want %q", got.Email, wantEmail)
	}

	list, err := svc.Projects.ServiceAccounts.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Accounts) != 1 {
		t.Fatalf("List returned %d accounts, want 1", len(list.Accounts))
	}

	if _, err := svc.Projects.ServiceAccounts.Delete(
		"projects/-/serviceAccounts/" + wantEmail,
	).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Projects.ServiceAccounts.Get(
		"projects/-/serviceAccounts/" + wantEmail,
	).Context(ctx).Do(); err == nil {
		t.Fatalf("Get after Delete: expected error, got nil")
	}
}

func TestSDKGCPIAMRoleLifecycle(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	created, err := svc.Projects.Roles.Create(parent, &iamv1.CreateRoleRequest{
		RoleId: "customViewer",
		Role: &iamv1.Role{
			Title:       "Custom Viewer",
			Description: "Read-only access",
			IncludedPermissions: []string{
				"compute.instances.list",
				"compute.instances.get",
			},
			Stage: "GA",
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantName := parent + "/roles/customViewer"
	if created.Name != wantName {
		t.Fatalf("got name %q, want %q", created.Name, wantName)
	}

	if len(created.IncludedPermissions) != 2 {
		t.Fatalf("got %d permissions, want 2", len(created.IncludedPermissions))
	}

	got, err := svc.Projects.Roles.Get(wantName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Title != "Custom Viewer" {
		t.Fatalf("Get returned title %q, want Custom Viewer", got.Title)
	}

	list, err := svc.Projects.Roles.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Roles) != 1 {
		t.Fatalf("List returned %d roles, want 1", len(list.Roles))
	}

	if _, err := svc.Projects.Roles.Delete(wantName).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestSDKGCPIAMServiceAccountKeysLifecycle(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	if _, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "key-owner",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	email := "key-owner@" + testProject + ".iam.gserviceaccount.com"
	saResource := "projects/-/serviceAccounts/" + email

	createdKey, err := svc.Projects.ServiceAccounts.Keys.Create(saResource,
		&iamv1.CreateServiceAccountKeyRequest{},
	).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Keys.Create: %v", err)
	}

	if createdKey.Name == "" {
		t.Fatalf("Keys.Create returned empty name")
	}

	if createdKey.PrivateKeyData == "" {
		t.Fatalf("Keys.Create did not return PrivateKeyData (real GCP returns it once)")
	}

	listed, err := svc.Projects.ServiceAccounts.Keys.List(saResource).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Keys.List: %v", err)
	}

	if len(listed.Keys) != 1 {
		t.Fatalf("Keys.List returned %d keys, want 1", len(listed.Keys))
	}

	if _, err := svc.Projects.ServiceAccounts.Keys.Delete(createdKey.Name).
		Context(ctx).Do(); err != nil {
		t.Fatalf("Keys.Delete: %v", err)
	}
}

func TestSDKGCPIAMNotFoundIsTyped(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	_, err := svc.Projects.ServiceAccounts.Get(
		"projects/-/serviceAccounts/ghost@demo-project.iam.gserviceaccount.com",
	).Context(ctx).Do()
	if err == nil {
		t.Fatalf("Get on missing SA: expected error, got nil")
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	if apiErr.Code != 404 {
		t.Fatalf("got HTTP %d, want 404", apiErr.Code)
	}
}
