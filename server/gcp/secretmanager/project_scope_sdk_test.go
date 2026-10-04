package secretmanager_test

import (
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
	sm "google.golang.org/api/secretmanager/v1"
)

func isHTTPStatus(err error, code int) bool {
	var gerr *googleapi.Error

	return errors.As(err, &gerr) && gerr.Code == code
}

func createScopedSecret(t *testing.T, svc *sm.Service, parent, id, value string) {
	t.Helper()

	if _, err := svc.Projects.Secrets.Create(parent, &sm.Secret{
		Replication: &sm.Replication{Automatic: &sm.Automatic{}},
		Labels:      map[string]string{"parent": parent[len("projects/"):]},
	}).SecretId(id).Do(); err != nil {
		t.Fatalf("create %s/secrets/%s: %v", parent, id, err)
	}

	if _, err := svc.Projects.Secrets.AddVersion(parent+"/secrets/"+id, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode(value)},
	}).Do(); err != nil {
		t.Fatalf("add version %s: %v", parent, err)
	}
}

// TestSecretSameIDTwoProjects pins GSM-01: a secret id is unique per project,
// so the same id in two projects is two independent secrets.
func TestSecretSameIDTwoProjects(t *testing.T) {
	svc := newSMService(t)

	const pa, pb = "projects/p-a", "projects/p-b"

	createScopedSecret(t, svc, pa, "sec1", "alpha")
	createScopedSecret(t, svc, pb, "sec1", "bravo")

	// p-b gets a second version; p-a's numbering must not move.
	if _, err := svc.Projects.Secrets.AddVersion(pb+"/secrets/sec1", &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("bravo-2")},
	}).Do(); err != nil {
		t.Fatalf("add second p-b version: %v", err)
	}

	for parent, want := range map[string]string{pa: "alpha", pb: "bravo-2"} {
		got, err := svc.Projects.Secrets.Get(parent + "/secrets/sec1").Do()
		if err != nil {
			t.Fatalf("get %s: %v", parent, err)
		}

		if got.Name != parent+"/secrets/sec1" || got.Labels["parent"] != parent[len("projects/"):] {
			t.Fatalf("get %s = %q labels %v", parent, got.Name, got.Labels)
		}

		acc, err := svc.Projects.Secrets.Versions.Access(parent + "/secrets/sec1/versions/latest").Do()
		if err != nil {
			t.Fatalf("access %s: %v", parent, err)
		}

		if acc.Payload.Data != encode(want) {
			t.Fatalf("access %s = %q, want %q", parent, acc.Payload.Data, encode(want))
		}
	}

	if _, err := svc.Projects.Secrets.Versions.Get(pa + "/secrets/sec1/versions/2").Do(); !isHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("p-a version 2: err = %v, want 404 (numbering is per secret)", err)
	}
}

func TestSecretIsolatedGetListDelete(t *testing.T) {
	svc := newSMService(t)

	const pa, pb = "projects/p-a", "projects/p-b"

	createScopedSecret(t, svc, pa, "only-a", "x")
	createScopedSecret(t, svc, pa, "sec1", "a")
	createScopedSecret(t, svc, pb, "sec1", "b")

	if _, err := svc.Projects.Secrets.Get(pb + "/secrets/only-a").Do(); !isHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("get p-a secret under p-b: err = %v, want 404", err)
	}

	if _, err := svc.Projects.Secrets.Versions.Access(pb + "/secrets/only-a/versions/latest").Do(); !isHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("access p-a secret under p-b: err = %v, want 404", err)
	}

	list, err := svc.Projects.Secrets.List(pb).Do()
	if err != nil {
		t.Fatalf("list p-b: %v", err)
	}

	if len(list.Secrets) != 1 || list.Secrets[0].Name != pb+"/secrets/sec1" {
		t.Fatalf("p-b secrets = %+v, want only sec1", list.Secrets)
	}

	if _, err := svc.Projects.Secrets.Delete(pb + "/secrets/sec1").Do(); err != nil {
		t.Fatalf("delete p-b: %v", err)
	}

	if _, err := svc.Projects.Secrets.Get(pa + "/secrets/sec1").Do(); err != nil {
		t.Fatalf("p-a secret after p-b delete: %v", err)
	}

	if _, err := svc.Projects.Secrets.Get(pb + "/secrets/sec1").Do(); !isHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("p-b secret after delete: err = %v, want 404", err)
	}
}
