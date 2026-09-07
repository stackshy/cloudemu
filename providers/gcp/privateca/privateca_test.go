package privateca

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func rawFields(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func mustCreatePool(t *testing.T, m *Mock, id string) {
	t.Helper()

	_, _, err := m.CreateCaPool(context.Background(), &pcadriver.Config{
		Project: "p", Location: "us-central1", ID: id, Fields: rawFields(map[string]string{"tier": "ENTERPRISE"}),
	})
	if err != nil {
		t.Fatalf("CreateCaPool: %v", err)
	}
}

func caField(t *testing.T, r *pcadriver.Resource, key string) string {
	t.Helper()

	var s string
	if err := json.Unmarshal(r.Fields[key], &s); err != nil {
		t.Fatalf("field %q not a string: %v", key, err)
	}

	return s
}

func TestCaPoolLifecycleAndDefaultTier(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// Tier defaults to ENTERPRISE when omitted.
	res, op, err := m.CreateCaPool(ctx, &pcadriver.Config{Project: "p", Location: "us-central1", ID: "pool"})
	if err != nil || !op.Done {
		t.Fatalf("CreateCaPool: %v op=%+v", err, op)
	}

	if got := caField(t, res, "tier"); got != "ENTERPRISE" {
		t.Fatalf("default tier = %q, want ENTERPRISE", got)
	}

	if _, err := m.DeleteCaPool(ctx, "p", "us-central1", "pool"); err != nil {
		t.Fatalf("DeleteCaPool: %v", err)
	}

	if _, err := m.GetCaPool(ctx, "p", "us-central1", "pool"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetCaPool after delete = %v, want NotFound", err)
	}
}

func TestDeleteNonEmptyCaPoolFailsPrecondition(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreatePool(t, m, "pool")

	if _, _, err := m.CreateCertificateAuthority(ctx, &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "pool", ID: "root",
		Fields: rawFields(map[string]string{"type": "SELF_SIGNED"}),
	}); err != nil {
		t.Fatalf("CreateCertificateAuthority: %v", err)
	}

	// A pool that still holds a CA cannot be deleted; the CA survives.
	if _, err := m.DeleteCaPool(ctx, "p", "us-central1", "pool"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("DeleteCaPool on non-empty pool = %v, want FailedPrecondition", err)
	}

	if _, err := m.GetCaPool(ctx, "p", "us-central1", "pool"); err != nil {
		t.Fatalf("GetCaPool after rejected delete = %v, want the pool to survive", err)
	}

	// Once the CA is soft-deleted out of the pool, the pool deletes cleanly.
	if _, err := m.DeleteCertificateAuthority(ctx, "p", "us-central1", "pool", "root"); err != nil {
		t.Fatalf("DeleteCertificateAuthority: %v", err)
	}

	if _, err := m.DeleteCaPool(ctx, "p", "us-central1", "pool"); err != nil {
		t.Fatalf("DeleteCaPool on empty pool: %v", err)
	}
}

func TestSelfSignedCAStateMachineAndPemStability(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreatePool(t, m, "pool")

	cfg := &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "pool", ID: "root",
		Fields: rawFields(map[string]string{"type": "SELF_SIGNED"}),
	}

	created, _, err := m.CreateCertificateAuthority(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateCertificateAuthority: %v", err)
	}

	if got := caField(t, created, "state"); got != stateStaged {
		t.Fatalf("self-signed initial state = %q, want STAGED", got)
	}

	pem := string(created.Fields["pemCaCertificates"])
	if pem == "" || pem == "[]" {
		t.Fatalf("self-signed CA missing pemCaCertificates: %q", pem)
	}

	// PEM is minted once and byte-stable across reads (no Terraform drift).
	got, err := m.GetCertificateAuthority(ctx, "p", "us-central1", "pool", "root")
	if err != nil {
		t.Fatalf("GetCertificateAuthority: %v", err)
	}

	if string(got.Fields["pemCaCertificates"]) != pem {
		t.Fatalf("pemCaCertificates drifted across reads")
	}

	// disable ENABLED -> DISABLED, then enable DISABLED -> ENABLED.
	if _, _, err := m.DisableCertificateAuthority(ctx, "p", "us-central1", "pool", "root"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	enabled, _, err := m.EnableCertificateAuthority(ctx, "p", "us-central1", "pool", "root")
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if got := caField(t, enabled, "state"); got != stateEnabled {
		t.Fatalf("state after enable = %q, want ENABLED", got)
	}

	// enable again is an illegal transition (already ENABLED).
	if _, _, err := m.EnableCertificateAuthority(ctx, "p", "us-central1", "pool", "root"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("Enable already-enabled = %v, want FailedPrecondition", err)
	}
}

func TestSubordinateCAActivateAndUndelete(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreatePool(t, m, "pool")

	cfg := &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "pool", ID: "sub",
		Fields: rawFields(map[string]string{"type": "SUBORDINATE"}),
	}

	created, _, err := m.CreateCertificateAuthority(ctx, cfg)
	if err != nil {
		t.Fatalf("Create subordinate: %v", err)
	}

	if got := caField(t, created, "state"); got != stateAwaitingUser {
		t.Fatalf("subordinate initial state = %q, want AWAITING_USER_ACTIVATION", got)
	}

	// fetch returns a deterministic CSR while awaiting activation.
	csr, err := m.FetchCertificateAuthorityCSR(ctx, "p", "us-central1", "pool", "sub")
	if err != nil || csr == "" {
		t.Fatalf("FetchCertificateAuthorityCSR: csr=%q err=%v", csr, err)
	}

	// activate requires a signed pem and moves AWAITING_USER_ACTIVATION -> ENABLED.
	if _, _, err := m.ActivateCertificateAuthority(ctx, "p", "us-central1", "pool", "sub", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("Activate without pem = %v, want InvalidArgument", err)
	}

	activated, _, err := m.ActivateCertificateAuthority(ctx, "p", "us-central1", "pool", "sub", "signed-pem")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if got := caField(t, activated, "state"); got != stateEnabled {
		t.Fatalf("state after activate = %q, want ENABLED", got)
	}

	// fetch after activation is illegal (no CSR to fetch).
	if _, err := m.FetchCertificateAuthorityCSR(ctx, "p", "us-central1", "pool", "sub"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("Fetch after activate = %v, want FailedPrecondition", err)
	}

	// soft-delete -> DELETED, then undelete -> DISABLED.
	if _, err := m.DeleteCertificateAuthority(ctx, "p", "us-central1", "pool", "sub"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	deleted, err := m.GetCertificateAuthority(ctx, "p", "us-central1", "pool", "sub")
	if err != nil {
		t.Fatalf("Get after soft-delete: %v", err)
	}

	if got := caField(t, deleted, "state"); got != stateDeleted {
		t.Fatalf("state after delete = %q, want DELETED", got)
	}

	undeleted, _, err := m.UndeleteCertificateAuthority(ctx, "p", "us-central1", "pool", "sub")
	if err != nil {
		t.Fatalf("Undelete: %v", err)
	}

	if got := caField(t, undeleted, "state"); got != stateDisabled {
		t.Fatalf("state after undelete = %q, want DISABLED", got)
	}
}

func TestCreateCAInMissingPoolIsNotFound(t *testing.T) {
	m := newMock(t)

	_, _, err := m.CreateCertificateAuthority(context.Background(), &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "ghost", ID: "root",
		Fields: rawFields(map[string]string{"type": "SELF_SIGNED"}),
	})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("CreateCertificateAuthority in missing pool = %v, want NotFound", err)
	}
}

func TestCertificateIssueAndRevoke(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreatePool(t, m, "pool")

	cfg := &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "pool", ID: "leaf",
		Fields: rawFields(map[string]string{"lifetime": "86400s"}),
	}

	created, err := m.CreateCertificate(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	pem := string(created.Fields["pemCertificate"])
	if pem == "" {
		t.Fatalf("certificate missing pemCertificate")
	}

	// pemCertificate is byte-stable across reads.
	got, err := m.GetCertificate(ctx, "p", "us-central1", "pool", "leaf")
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if string(got.Fields["pemCertificate"]) != pem {
		t.Fatalf("pemCertificate drifted across reads")
	}

	// revoke sets revocationDetails; a second revoke is rejected.
	revoked, err := m.RevokeCertificate(ctx, "p", "us-central1", "pool", "leaf", "KEY_COMPROMISE")
	if err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	if _, ok := revoked.Fields["revocationDetails"]; !ok {
		t.Fatalf("revocationDetails not set after revoke")
	}

	if _, err := m.RevokeCertificate(ctx, "p", "us-central1", "pool", "leaf", "KEY_COMPROMISE"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("second revoke = %v, want FailedPrecondition", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreatePool(t, m, "pool")

	if _, _, err := m.CreateCertificateAuthority(ctx, &pcadriver.Config{
		Project: "p", Location: "us-central1", CaPool: "pool", ID: "root",
		Fields: rawFields(map[string]string{"type": "SELF_SIGNED"}),
	}); err != nil {
		t.Fatalf("CreateCertificateAuthority: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	ca, err := restored.GetCertificateAuthority(ctx, "p", "us-central1", "pool", "root")
	if err != nil {
		t.Fatalf("GetCertificateAuthority after restore: %v", err)
	}

	if got := caField(t, ca, "state"); got != stateStaged {
		t.Fatalf("restored CA state = %q, want STAGED", got)
	}
}
