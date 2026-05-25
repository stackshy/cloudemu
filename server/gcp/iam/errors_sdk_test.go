package iam_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"
)

// google.golang.org/api/iam/v1 surfaces handler errors as *googleapi.Error.
// This file locks in the StatusCode contract for the paths the main
// lifecycle tests don't cover.

func assertGoogleAPIError(t *testing.T, err error, wantCode int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	if apiErr.Code != wantCode {
		t.Fatalf("got HTTP %d, want %d", apiErr.Code, wantCode)
	}
}

func TestSDKGCPIAMRoleNotFoundIsTyped(t *testing.T) {
	svc := newSDKService(t)

	_, err := svc.Projects.Roles.Get(
		"projects/" + testProject + "/roles/no-such-role",
	).Context(context.Background()).Do()

	assertGoogleAPIError(t, err, 404)
}

func TestSDKGCPIAMKeyNotFoundIsTyped(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject
	if _, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "key-test",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	email := "key-test@" + testProject + ".iam.gserviceaccount.com"

	_, err := svc.Projects.ServiceAccounts.Keys.Get(
		"projects/-/serviceAccounts/" + email + "/keys/no-such-key",
	).Context(ctx).Do()

	assertGoogleAPIError(t, err, 404)
}

func TestSDKGCPIAMDuplicateServiceAccountIsConflict(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	if _, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "dupe",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "dupe",
	}).Context(ctx).Do()

	assertGoogleAPIError(t, err, 409)
}

// TestSDKGCPIAMPatchActuallyReplaces verifies the Patch wire-shape decode
// works: the SDK wraps the payload in {"serviceAccount": {...}, "updateMask":
// "..."}, and a previous version of the handler decoded into a bare
// serviceAccount struct which silently dropped every field. This test would
// have caught that regression.
func TestSDKGCPIAMPatchActuallyReplaces(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	if _, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "patcher",
		ServiceAccount: &iamv1.ServiceAccount{
			DisplayName: "before",
			Description: "old description",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	email := "patcher@" + testProject + ".iam.gserviceaccount.com"
	saResource := "projects/-/serviceAccounts/" + email

	if _, err := svc.Projects.ServiceAccounts.Patch(saResource,
		&iamv1.PatchServiceAccountRequest{
			ServiceAccount: &iamv1.ServiceAccount{
				DisplayName: "after",
				Description: "new description",
			},
			UpdateMask: "displayName,description",
		},
	).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.ServiceAccounts.Get(saResource).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after Patch: %v", err)
	}

	if got.DisplayName != "after" {
		t.Fatalf("got displayName %q, want after", got.DisplayName)
	}

	if got.Description != "new description" {
		t.Fatalf("got description %q, want new description", got.Description)
	}
}

// TestSDKGCPIAMPatchPreservesProjectThroughWildcard verifies that a PATCH
// using the projects/- wildcard URL preserves the SA's original project
// rather than moving the SA into a "-" project bucket — which would then
// vanish from list under the real project.
func TestSDKGCPIAMPatchPreservesProjectThroughWildcard(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	if _, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: "wildcard-patch",
		ServiceAccount: &iamv1.ServiceAccount{
			DisplayName: "original",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	email := "wildcard-patch@" + testProject + ".iam.gserviceaccount.com"

	if _, err := svc.Projects.ServiceAccounts.Patch(
		"projects/-/serviceAccounts/"+email,
		&iamv1.PatchServiceAccountRequest{
			ServiceAccount: &iamv1.ServiceAccount{DisplayName: "patched"},
			UpdateMask:     "displayName",
		},
	).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch via wildcard: %v", err)
	}

	// The SA must still surface under the real project after a wildcard
	// patch — proving updateServiceAccount preserved the stored project.
	list, err := svc.Projects.ServiceAccounts.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	found := false
	for _, a := range list.Accounts {
		if a.Email == email {
			found = true
			if a.DisplayName != "patched" {
				t.Fatalf("got displayName %q, want patched", a.DisplayName)
			}
		}
	}
	if !found {
		t.Fatalf("SA disappeared from project listing after wildcard patch")
	}
}
