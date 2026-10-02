package managedidentity_test

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
)

func ficIn(subject string) managedidentity.FICInput {
	return managedidentity.FICInput{Issuer: "https://issuer.example", Subject: subject,
		Audiences: []string{"api://AzureADTokenExchange"}}
}

func TestFICLifecycleAndBoundedCascade(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, id := range []string{"id1", "id10"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", id, managedidentity.Input{Location: "eastus"}); err != nil {
			t.Fatal(err)
		}

		if _, created, err := m.CreateOrUpdateFIC(ctx, "sub", "rg", id, "fic-a", ficIn(id)); err != nil || !created {
			t.Fatalf("create %s: created=%v err=%v", id, created, err)
		}
	}

	if _, created, err := m.CreateOrUpdateFIC(ctx, "sub", "rg", "id1", "fic-a", ficIn("id1")); err != nil || created {
		t.Errorf("update: created=%v err=%v", created, err)
	}

	if _, _, err := m.CreateOrUpdateFIC(ctx, "sub", "rg", "id1", "fic-b", ficIn("id1")); !cerrors.IsAlreadyExists(err) {
		t.Errorf("duplicate issuer/subject err = %v, want AlreadyExists", err)
	}

	if _, _, err := m.CreateOrUpdateFIC(ctx, "sub", "rg", "ghost", "fic-a", ficIn("x")); !cerrors.IsNotFound(err) {
		t.Errorf("missing identity err = %v, want NotFound", err)
	}

	if _, err := m.Delete(ctx, "sub", "rg", "id1"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetFIC(ctx, "sub", "rg", "id1", "fic-a"); !cerrors.IsNotFound(err) {
		t.Errorf("id1 credential survived its identity: %v", err)
	}

	if list, err := m.ListFICs(ctx, "sub", "rg", "id10"); err != nil || len(list) != 1 {
		t.Errorf("id10 credentials = %v, %v; want 1 kept", list, err)
	}

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetFIC(ctx, "sub", "rg", "id10", "fic-a"); !cerrors.IsNotFound(err) {
		t.Errorf("RG purge left a credential: %v", err)
	}
}

func TestFICSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "id1", managedidentity.Input{Location: "eastus"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.CreateOrUpdateFIC(ctx, "sub", "rg", "id1", "fic-a", ficIn("s")); err != nil {
		t.Fatal(err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	r := newMock()
	if err := r.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	f, err := r.GetFIC(ctx, "sub", "rg", "id1", "fic-a")
	if err != nil || f.Subject != "s" || len(f.Audiences) != 1 {
		t.Errorf("restored credential = %+v, %v", f, err)
	}
}
