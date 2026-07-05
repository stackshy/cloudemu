package secretmanager_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	sm "google.golang.org/api/secretmanager/v1"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const testParent = "projects/demo"

func newSMService(t *testing.T) *sm.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{SecretManager: cloud.SecretManager})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := sm.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("secretmanager.NewService: %v", err)
	}

	return svc
}

func encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestSDKSecretManagerLifecycle(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	created, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
		Labels:      map[string]string{"env": "test"},
	}).SecretId("db-password").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.Name != testParent+"/secrets/db-password" {
		t.Fatalf("got name %q", created.Name)
	}

	got, err := svc.Projects.Secrets.Get(testParent + "/secrets/db-password").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["env"] != "test" {
		t.Fatalf("labels = %v, want env=test", got.Labels)
	}

	if got.Replication == nil || got.Replication.Automatic == nil {
		t.Fatalf("replication = %+v, want automatic", got.Replication)
	}

	list, err := svc.Projects.Secrets.List(testParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Secrets) != 1 || list.Secrets[0].Name != created.Name {
		t.Fatalf("List = %+v, want one secret %s", list.Secrets, created.Name)
	}

	if _, err := svc.Projects.Secrets.Delete(created.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = svc.Projects.Secrets.Get(created.Name).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKSecretManagerVersionsAndAccess(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	name := testParent + "/secrets/api-key"

	if _, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
	}).SecretId("api-key").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	v1, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("v1-value")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion(v1): %v", err)
	}

	v2, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("v2-value")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion(v2): %v", err)
	}

	if v1.Name == v2.Name {
		t.Fatal("AddVersion reused a version name")
	}

	latest, err := svc.Projects.Secrets.Versions.Access(name + "/versions/latest").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access(latest): %v", err)
	}

	if latest.Payload.Data != encode("v2-value") {
		t.Fatalf("latest = %q, want %q", latest.Payload.Data, encode("v2-value"))
	}

	old, err := svc.Projects.Secrets.Versions.Access(v1.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access(v1): %v", err)
	}

	if old.Payload.Data != encode("v1-value") {
		t.Fatalf("v1 = %q, want %q", old.Payload.Data, encode("v1-value"))
	}

	meta, err := svc.Projects.Secrets.Versions.Get(v1.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Versions.Get: %v", err)
	}

	if meta.State != "ENABLED" {
		t.Fatalf("state = %q, want ENABLED", meta.State)
	}

	versions, err := svc.Projects.Secrets.Versions.List(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Versions.List: %v", err)
	}

	// The driver seeds an initial version on create, so two AddVersion calls
	// yield three versions.
	if len(versions.Versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions.Versions))
	}
}

func TestSDKSecretManagerErrors(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	_, err := svc.Projects.Secrets.Get(testParent + "/secrets/missing").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}

	if _, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
	}).SecretId("dup").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
	}).SecretId("dup").Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 409 {
		t.Fatalf("duplicate Create: got %v, want 409", err)
	}

	_, err = svc.Projects.Secrets.Versions.Access(testParent + "/secrets/missing/versions/latest").Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Access(missing): got %v, want 404", err)
	}
}
