package iam_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"
)

// createSA is a small helper: creates an SA and returns its email.
func createSA(t *testing.T, svc *iamv1.Service, accountID string) string {
	t.Helper()

	parent := "projects/" + testProject

	sa, err := svc.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
		AccountId: accountID,
	}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Create %s: %v", accountID, err)
	}

	return sa.Email
}

// TestSDKGCPIAMTestIamPermissions guards the HIGH finding: TestIamPermissions
// on an SA must return the held subset of requested permissions, not 404.
func TestSDKGCPIAMTestIamPermissions(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	email := createSA(t, svc, "perm-sa")
	resource := "projects/" + testProject + "/serviceAccounts/" + email

	resp, err := svc.Projects.ServiceAccounts.TestIamPermissions(resource,
		&iamv1.TestIamPermissionsRequest{
			Permissions: []string{"iam.serviceAccounts.actAs", "iam.serviceAccounts.get"},
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	// With no restricting policy the resource owner holds every requested perm.
	if len(resp.Permissions) != 2 {
		t.Fatalf("got %d held permissions, want 2: %v", len(resp.Permissions), resp.Permissions)
	}
}

// TestSDKGCPIAMTestIamPermissionsSubset checks that a bound custom role narrows
// the held set to exactly that role's includedPermissions.
func TestSDKGCPIAMTestIamPermissionsSubset(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject

	if _, err := svc.Projects.Roles.Create(parent, &iamv1.CreateRoleRequest{
		RoleId: "actAsOnly",
		Role: &iamv1.Role{
			Title:               "Act As Only",
			IncludedPermissions: []string{"iam.serviceAccounts.actAs"},
			Stage:               "GA",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	email := createSA(t, svc, "subset-sa")
	resource := "projects/" + testProject + "/serviceAccounts/" + email

	if _, err := svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{
			Bindings: []*iamv1.Binding{{
				Role:    parent + "/roles/actAsOnly",
				Members: []string{"user:alice@example.com"},
			}},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	resp, err := svc.Projects.ServiceAccounts.TestIamPermissions(resource,
		&iamv1.TestIamPermissionsRequest{
			Permissions: []string{"iam.serviceAccounts.actAs", "iam.serviceAccounts.delete"},
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(resp.Permissions) != 1 || resp.Permissions[0] != "iam.serviceAccounts.actAs" {
		t.Fatalf("expected only actAs held, got %v", resp.Permissions)
	}
}

// TestSDKGCPIAMServiceAccountUndelete guards the MEDIUM finding: a recently
// deleted SA can be restored via Undelete.
func TestSDKGCPIAMServiceAccountUndelete(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	email := createSA(t, svc, "restore-me")
	name := "projects/-/serviceAccounts/" + email

	if _, err := svc.Projects.ServiceAccounts.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	resp, err := svc.Projects.ServiceAccounts.Undelete(name,
		&iamv1.UndeleteServiceAccountRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Undelete: %v", err)
	}

	if resp.RestoredAccount == nil || resp.RestoredAccount.Email != email {
		t.Fatalf("Undelete did not return restored account: %+v", resp.RestoredAccount)
	}

	// The account is queryable again after restore.
	if _, err := svc.Projects.ServiceAccounts.Get(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Get after Undelete: %v", err)
	}
}

// TestSDKGCPIAMRoleUndelete guards the MEDIUM finding: Roles.Undelete restores
// a deleted custom role (routeRoles previously ignored the verb → 405).
func TestSDKGCPIAMRoleUndelete(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	parent := "projects/" + testProject
	name := parent + "/roles/undeleteMe"

	if _, err := svc.Projects.Roles.Create(parent, &iamv1.CreateRoleRequest{
		RoleId: "undeleteMe",
		Role: &iamv1.Role{
			Title:               "Undelete Me",
			IncludedPermissions: []string{"compute.instances.get"},
			Stage:               "GA",
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	if _, err := svc.Projects.Roles.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete role: %v", err)
	}

	restored, err := svc.Projects.Roles.Undelete(name, &iamv1.UndeleteRoleRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Undelete role: %v", err)
	}

	if restored.Name != name {
		t.Fatalf("restored role name %q, want %q", restored.Name, name)
	}

	got, err := svc.Projects.Roles.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after Undelete: %v", err)
	}

	if len(got.IncludedPermissions) != 1 {
		t.Fatalf("restored role lost permissions: %v", got.IncludedPermissions)
	}
}

var uniqueIDRe = regexp.MustCompile(`^[1-9][0-9]{20}$`)

// TestSDKGCPIAMServiceAccountIdentity guards the MEDIUM finding: uniqueId is a
// stable 21-digit numeric, oauth2ClientId equals it, and etag is populated.
func TestSDKGCPIAMServiceAccountIdentity(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	created := func() *iamv1.ServiceAccount {
		sa, err := svc.Projects.ServiceAccounts.Create("projects/"+testProject,
			&iamv1.CreateServiceAccountRequest{AccountId: "identity-sa"}).Context(ctx).Do()
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		return sa
	}()

	if !uniqueIDRe.MatchString(created.UniqueId) {
		t.Fatalf("uniqueId %q is not a 21-digit numeric", created.UniqueId)
	}

	if created.Oauth2ClientId != created.UniqueId {
		t.Fatalf("oauth2ClientId %q != uniqueId %q", created.Oauth2ClientId, created.UniqueId)
	}

	if created.Etag == "" {
		t.Fatal("etag is empty, want populated")
	}

	// uniqueId is stable across Get.
	got, err := svc.Projects.ServiceAccounts.Get(
		"projects/-/serviceAccounts/" + created.Email).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.UniqueId != created.UniqueId {
		t.Fatalf("uniqueId not stable: create=%q get=%q", created.UniqueId, got.UniqueId)
	}
}

// TestSDKGCPIAMListReflectsDisabled guards the MEDIUM finding: List must
// reflect the disabled bit, not just Get.
func TestSDKGCPIAMListReflectsDisabled(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	email := createSA(t, svc, "toggle-sa")
	resource := "projects/" + testProject + "/serviceAccounts/" + email

	if _, err := svc.Projects.ServiceAccounts.Disable(resource,
		&iamv1.DisableServiceAccountRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	list, err := svc.Projects.ServiceAccounts.List("projects/" + testProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var found *iamv1.ServiceAccount

	for _, sa := range list.Accounts {
		if sa.Email == email {
			found = sa
			break
		}
	}

	if found == nil {
		t.Fatalf("SA %s missing from list", email)
	}

	if !found.Disabled {
		t.Fatal("List did not reflect disabled=true")
	}
}

// TestSDKGCPIAMKeyFileAndValidity guards the MEDIUM finding: Keys.Create returns
// a base64 credentials file (parseable service_account JSON) and populated
// validAfterTime/validBeforeTime.
func TestSDKGCPIAMKeyFileAndValidity(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	email := createSA(t, svc, "key-file-sa")
	resource := "projects/-/serviceAccounts/" + email

	key, err := svc.Projects.ServiceAccounts.Keys.Create(resource,
		&iamv1.CreateServiceAccountKeyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Keys.Create: %v", err)
	}

	if key.ValidAfterTime == "" || key.ValidBeforeTime == "" {
		t.Fatalf("validity window empty: after=%q before=%q", key.ValidAfterTime, key.ValidBeforeTime)
	}

	raw, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		t.Fatalf("privateKeyData is not base64: %v", err)
	}

	var keyFile map[string]any
	if err := json.Unmarshal(raw, &keyFile); err != nil {
		t.Fatalf("privateKeyData is not a JSON credentials file: %v", err)
	}

	if keyFile["type"] != "service_account" {
		t.Fatalf("key file type = %v, want service_account", keyFile["type"])
	}

	if keyFile["client_email"] != email {
		t.Fatalf("key file client_email = %v, want %s", keyFile["client_email"], email)
	}

	if _, ok := keyFile["private_key"]; !ok {
		t.Fatal("key file missing private_key field")
	}
}

// TestSDKGCPIAMSetIamPolicyEtagConcurrency guards the LOW finding: a stale etag
// on setIamPolicy is rejected with 409 ABORTED (optimistic concurrency).
func TestSDKGCPIAMSetIamPolicyEtagConcurrency(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	email := createSA(t, svc, "etag-sa")
	resource := "projects/" + testProject + "/serviceAccounts/" + email

	first, err := svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{
			Bindings: []*iamv1.Binding{{Role: "roles/iam.serviceAccountUser", Members: []string{"user:a@x.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy #1: %v", err)
	}

	if first.Etag == "" {
		t.Fatal("first policy returned empty etag")
	}

	// A write carrying the fresh etag succeeds and advances the etag.
	second, err := svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{
			Etag:     first.Etag,
			Bindings: []*iamv1.Binding{{Role: "roles/viewer", Members: []string{"user:b@x.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy #2 (fresh etag): %v", err)
	}

	if second.Etag == first.Etag {
		t.Fatal("etag did not advance after a successful write")
	}

	// Replaying the now-stale first etag must be rejected with 409 ABORTED.
	_, err = svc.Projects.ServiceAccounts.SetIamPolicy(resource, &iamv1.SetIamPolicyRequest{
		Policy: &iamv1.Policy{
			Etag:     first.Etag,
			Bindings: []*iamv1.Binding{{Role: "roles/editor", Members: []string{"user:c@x.com"}}},
		},
	}).Context(ctx).Do()
	if err == nil {
		t.Fatal("stale-etag SetIamPolicy: expected error, got nil")
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *googleapi.Error, got %T: %v", err, err)
	}

	if apiErr.Code != 409 {
		t.Fatalf("stale-etag: got HTTP %d, want 409", apiErr.Code)
	}
}

// TestSDKGCPIAMListPagination guards the LOW finding: List honors
// pageSize/pageToken and returns nextPageToken, and roles carry an etag.
func TestSDKGCPIAMListPagination(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	for _, id := range []string{"page-a", "page-b", "page-c"} {
		createSA(t, svc, id)
	}

	first, err := svc.Projects.ServiceAccounts.List("projects/" + testProject).
		PageSize(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}

	if len(first.Accounts) != 2 {
		t.Fatalf("page 1 returned %d accounts, want 2", len(first.Accounts))
	}

	if first.NextPageToken == "" {
		t.Fatal("page 1 missing nextPageToken")
	}

	second, err := svc.Projects.ServiceAccounts.List("projects/" + testProject).
		PageSize(2).PageToken(first.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if len(second.Accounts) != 1 {
		t.Fatalf("page 2 returned %d accounts, want 1", len(second.Accounts))
	}

	if second.NextPageToken != "" {
		t.Fatal("page 2 should be the last page (empty nextPageToken)")
	}

	// Roles carry a populated etag.
	if _, err := svc.Projects.Roles.Create("projects/"+testProject, &iamv1.CreateRoleRequest{
		RoleId: "etag-role",
		Role:   &iamv1.Role{Title: "Etag Role", Stage: "GA"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	got, err := svc.Projects.Roles.Get("projects/" + testProject + "/roles/etag-role").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get role: %v", err)
	}

	if got.Etag == "" {
		t.Fatal("role etag is empty, want populated")
	}
}
