package secretmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestSecretIAMPolicyEtag: an unset policy reads with a stable etag, every set
// mints a new one, a stale etag is ErrAborted and an empty etag is a blind
// overwrite (T4-13).
func TestSecretIAMPolicyEtag(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "iam-etag", "v")
	ctx := context.Background()

	first, _ := m.GetSecretIAMPolicy(ctx, info.Name)
	second, _ := m.GetSecretIAMPolicy(ctx, info.Name)

	if first.Etag != second.Etag {
		t.Fatalf("unset policy etag not stable: %q vs %q", first.Etag, second.Etag)
	}

	bind := []driver.GCPIAMBinding{{Role: "roles/secretmanager.secretAccessor", Members: []string{"user:x@y.com"}}}

	set, err := m.SetSecretIAMPolicy(ctx, info.Name, driver.GCPIAMPolicy{Bindings: bind, Etag: first.Etag})
	if err != nil {
		t.Fatalf("SetSecretIAMPolicy: %v", err)
	}

	if set.Etag == first.Etag {
		t.Fatal("etag did not change on set")
	}

	_, err = m.SetSecretIAMPolicy(ctx, info.Name, driver.GCPIAMPolicy{Etag: first.Etag})
	if !errors.Is(err, resourceiam.ErrAborted) {
		t.Fatalf("stale etag = %v, want ErrAborted", err)
	}

	blind, err := m.SetSecretIAMPolicy(ctx, info.Name, driver.GCPIAMPolicy{})
	if err != nil {
		t.Fatalf("blind SetSecretIAMPolicy: %v", err)
	}

	if blind.Etag == set.Etag || len(blind.Bindings) != 0 {
		t.Fatalf("blind write = %+v, want a fresh etag and no bindings", blind)
	}
}
