package binaryauthorization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"
	"github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

// TestAttestorIAMPolicyEtag: an unset policy reads with a stable etag, every
// set mints a new one, a stale etag is ErrAborted and an empty etag is a blind
// overwrite (T4-13).
func TestAttestorIAMPolicyEtag(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	cfg := driver.AttestorConfig{UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "n"}}

	a, err := m.CreateAttestor(ctx, project, "etag", cfg)
	if err != nil {
		t.Fatalf("CreateAttestor: %v", err)
	}

	first, _ := m.GetIamPolicy(ctx, a.Name)
	second, _ := m.GetIamPolicy(ctx, a.Name)

	if first.Etag != second.Etag {
		t.Fatalf("unset policy etag not stable: %q vs %q", first.Etag, second.Etag)
	}

	bind := []driver.IAMBinding{{Role: "roles/viewer", Members: []string{"user:x@y.com"}}}

	set, err := m.SetIamPolicy(ctx, a.Name, driver.IAMPolicy{Bindings: bind, Etag: first.Etag})
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == first.Etag {
		t.Fatal("etag did not change on set")
	}

	if _, err := m.SetIamPolicy(ctx, a.Name, driver.IAMPolicy{Etag: first.Etag}); !errors.Is(err, resourceiam.ErrAborted) {
		t.Fatalf("stale etag = %v, want ErrAborted", err)
	}

	blind, err := m.SetIamPolicy(ctx, a.Name, driver.IAMPolicy{})
	if err != nil {
		t.Fatalf("blind SetIamPolicy: %v", err)
	}

	if blind.Etag == set.Etag || len(blind.Bindings) != 0 {
		t.Fatalf("blind write = %+v, want a fresh etag and no bindings", blind)
	}
}
