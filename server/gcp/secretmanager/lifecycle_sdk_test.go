package secretmanager_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	sm "google.golang.org/api/secretmanager/v1"
)

// mustCreateSecret creates an empty (GCP-style) secret container.
func mustCreateSecret(t *testing.T, svc *sm.Service, id string) string {
	t.Helper()

	if _, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
	}).SecretId(id).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}

	return testParent + "/secrets/" + id
}

// TestSDKVersionIntegerNaming proves versions are numbered 1,2,3… and are
// addressable by their integer id (audit: AddSecretVersion version naming).
func TestSDKVersionIntegerNaming(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "int-named")

	v1, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("one")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if !strings.HasSuffix(v1.Name, "/versions/1") {
		t.Fatalf("v1 name = %q, want .../versions/1", v1.Name)
	}

	// Address the version by its integer id.
	got, err := svc.Projects.Secrets.Versions.Access(name + "/versions/1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access(versions/1): %v", err)
	}

	if got.Payload.Data != encode("one") {
		t.Fatalf("payload = %q, want %q", got.Payload.Data, encode("one"))
	}

	v2, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("two")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion(2): %v", err)
	}

	if !strings.HasSuffix(v2.Name, "/versions/2") {
		t.Fatalf("v2 name = %q, want .../versions/2", v2.Name)
	}
}

// TestSDKVersionLifecycle proves disable/enable/destroy transition state and
// gate :access (audit: destroy, disable/enable, per-version state).
func TestSDKVersionLifecycle(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "lifecycle")

	v1, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("secret")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	// Disable → state DISABLED, access blocked with FAILED_PRECONDITION (400).
	dis, err := svc.Projects.Secrets.Versions.Disable(v1.Name, &sm.DisableSecretVersionRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if dis.State != "DISABLED" {
		t.Fatalf("state after Disable = %q, want DISABLED", dis.State)
	}

	_, err = svc.Projects.Secrets.Versions.Access(v1.Name).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("Access(disabled): got %v, want 400 FAILED_PRECONDITION", err)
	}

	// Get still reports metadata for a disabled version.
	meta, err := svc.Projects.Secrets.Versions.Get(v1.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(disabled): %v", err)
	}

	if meta.State != "DISABLED" {
		t.Fatalf("Get state = %q, want DISABLED", meta.State)
	}

	// Enable → access works again.
	en, err := svc.Projects.Secrets.Versions.Enable(v1.Name, &sm.EnableSecretVersionRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if en.State != "ENABLED" {
		t.Fatalf("state after Enable = %q, want ENABLED", en.State)
	}

	if _, err := svc.Projects.Secrets.Versions.Access(v1.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Access after Enable: %v", err)
	}

	// Destroy → state DESTROYED, destroyTime stamped, payload gone, access fails.
	des, err := svc.Projects.Secrets.Versions.Destroy(v1.Name, &sm.DestroySecretVersionRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if des.State != "DESTROYED" {
		t.Fatalf("state after Destroy = %q, want DESTROYED", des.State)
	}

	if des.DestroyTime == "" {
		t.Fatal("DestroyTime empty after Destroy, want a timestamp")
	}

	_, err = svc.Projects.Secrets.Versions.Access(v1.Name).Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("Access(destroyed): got %v, want 400 FAILED_PRECONDITION", err)
	}
}

// TestSDKSecretPatch proves secrets.patch updates labels (audit: Secrets.patch).
func TestSDKSecretPatch(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "patch-me")

	updated, err := svc.Projects.Secrets.Patch(name, &sm.Secret{
		Labels: map[string]string{"team": "platform", "env": "prod"},
	}).UpdateMask("labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if updated.Labels["team"] != "platform" || updated.Labels["env"] != "prod" {
		t.Fatalf("labels = %v, want team=platform env=prod", updated.Labels)
	}

	got, err := svc.Projects.Secrets.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["team"] != "platform" {
		t.Fatalf("persisted labels = %v, want team=platform", got.Labels)
	}
}

// TestSDKSecretIAM proves getIamPolicy/setIamPolicy/testIamPermissions round-
// trip (audit: Secrets IAM).
func TestSDKSecretIAM(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "iam-me")

	// getIamPolicy on an existing secret with no policy returns an empty policy.
	empty, err := svc.Projects.Secrets.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy(empty): %v", err)
	}

	if len(empty.Bindings) != 0 {
		t.Fatalf("empty policy has %d bindings, want 0", len(empty.Bindings))
	}

	set, err := svc.Projects.Secrets.SetIamPolicy(name, &sm.SetIamPolicyRequest{
		Policy: &sm.Policy{
			Bindings: []*sm.Binding{{
				Role:    "roles/secretmanager.secretAccessor",
				Members: []string{"user:alice@example.com"},
			}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Bindings[0].Members[0] != "user:alice@example.com" {
		t.Fatalf("set policy = %+v, want alice binding", set.Bindings)
	}

	got, err := svc.Projects.Secrets.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/secretmanager.secretAccessor" {
		t.Fatalf("round-tripped policy = %+v, want accessor binding", got.Bindings)
	}

	tip, err := svc.Projects.Secrets.TestIamPermissions(name, &sm.TestIamPermissionsRequest{
		Permissions: []string{"secretmanager.versions.access"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(tip.Permissions) != 1 || tip.Permissions[0] != "secretmanager.versions.access" {
		t.Fatalf("granted = %v, want the requested permission", tip.Permissions)
	}
}

// TestSDKSecretListPagination proves pageSize/pageToken page the secrets list
// (audit: Secrets.list pagination).
func TestSDKSecretListPagination(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	const total = 6
	for i := 0; i < total; i++ {
		mustCreateSecret(t, svc, fmt.Sprintf("paged-%d", i))
	}

	first, err := svc.Projects.Secrets.List(testParent).PageSize(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(page 1): %v", err)
	}

	if len(first.Secrets) != 2 {
		t.Fatalf("page 1 = %d secrets, want 2", len(first.Secrets))
	}

	if first.NextPageToken == "" {
		t.Fatal("page 1 NextPageToken empty, want a continuation token")
	}

	// Walk every page and count the distinct secrets returned.
	seen := map[string]bool{}
	token := ""

	for {
		page, err := svc.Projects.Secrets.List(testParent).PageSize(2).PageToken(token).Context(ctx).Do()
		if err != nil {
			t.Fatalf("List(page token=%q): %v", token, err)
		}

		if len(page.Secrets) > 2 {
			t.Fatalf("page returned %d secrets, want <= pageSize 2", len(page.Secrets))
		}

		for _, s := range page.Secrets {
			seen[s.Name] = true
		}

		if page.NextPageToken == "" {
			break
		}

		token = page.NextPageToken
	}

	if len(seen) != total {
		t.Fatalf("paged through %d distinct secrets, want %d", len(seen), total)
	}
}

// TestSDKVersionFields proves versions carry etag and replicationStatus, and a
// live version has no destroyTime (audit: Secret/SecretVersion fields).
func TestSDKVersionFields(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "fields")

	v1, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("x")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if v1.Etag == "" {
		t.Fatal("version Etag empty, want an opaque tag")
	}

	if v1.ReplicationStatus == nil || v1.ReplicationStatus.Automatic == nil {
		t.Fatalf("replicationStatus = %+v, want automatic", v1.ReplicationStatus)
	}

	if v1.DestroyTime != "" {
		t.Fatalf("DestroyTime = %q on a live version, want empty", v1.DestroyTime)
	}

	sec, err := svc.Projects.Secrets.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get secret: %v", err)
	}

	if sec.Etag == "" {
		t.Fatal("secret Etag empty, want an opaque tag")
	}
}
