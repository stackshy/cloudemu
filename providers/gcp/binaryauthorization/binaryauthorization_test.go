package binaryauthorization_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/binaryauthorization"
	"github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

const project = "demo"

func newMock() *binaryauthorization.Mock {
	return binaryauthorization.New(config.NewOptions())
}

func TestGetPolicySeedsStableDefault(t *testing.T) {
	m := newMock()

	p, err := m.GetPolicy(context.Background(), project)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if p.Name != "projects/demo/policy" {
		t.Fatalf("name = %q, want projects/demo/policy", p.Name)
	}

	if p.GlobalPolicyEvaluationMode != driver.GlobalPolicyEvaluationModeEnable {
		t.Fatalf("globalPolicyEvaluationMode = %q, want ENABLE", p.GlobalPolicyEvaluationMode)
	}

	if p.UpdateTime.IsZero() {
		t.Fatalf("updateTime not minted on seeded default")
	}

	again, err := m.GetPolicy(context.Background(), project)
	if err != nil {
		t.Fatalf("GetPolicy(2): %v", err)
	}

	// The default is stored on first read, so name/updateTime/etag are stable.
	if !again.UpdateTime.Equal(p.UpdateTime) || again.Etag != p.Etag {
		t.Fatalf("default policy not stable across reads")
	}
}

func TestUpdatePolicyFullReplaceRoundTrip(t *testing.T) {
	m := newMock()

	rule := json.RawMessage(`{"evaluationMode":"REQUIRE_ATTESTATION","enforcementMode":"DRYRUN_AUDIT_LOG_ONLY"}`)

	p, err := m.UpdatePolicy(context.Background(), project, &driver.PolicyConfig{
		Description:                "prod",
		GlobalPolicyEvaluationMode: driver.GlobalPolicyEvaluationModeDisable,
		DefaultAdmissionRule:       rule,
	})
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	if string(p.DefaultAdmissionRule) != string(rule) {
		t.Fatalf("defaultAdmissionRule = %s, want verbatim %s", p.DefaultAdmissionRule, rule)
	}

	got, err := m.GetPolicy(context.Background(), project)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if got.Description != "prod" || string(got.DefaultAdmissionRule) != string(rule) {
		t.Fatalf("policy not round-tripped: %+v", got)
	}
}

func TestUpdatePolicyReadDoesNotMutateStored(t *testing.T) {
	m := newMock()

	rule := json.RawMessage(`{"evaluationMode":"ALWAYS_DENY"}`)
	if _, err := m.UpdatePolicy(context.Background(), project, &driver.PolicyConfig{DefaultAdmissionRule: rule}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	got, _ := m.GetPolicy(context.Background(), project)
	// Mutating the returned deep block must not corrupt stored state.
	got.DefaultAdmissionRule[0] = 'X'

	again, _ := m.GetPolicy(context.Background(), project)
	if string(again.DefaultAdmissionRule) != string(rule) {
		t.Fatalf("stored block aliased to returned copy: %s", again.DefaultAdmissionRule)
	}
}

func TestAttestorCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	a, err := m.CreateAttestor(ctx, project, "prod", driver.AttestorConfig{
		Description: "prod attestor",
		UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{
			NoteReference: "projects/demo/notes/n",
			PublicKeys:    json.RawMessage(`[{"id":"k1"}]`),
		},
	})
	if err != nil {
		t.Fatalf("CreateAttestor: %v", err)
	}

	if a.Name != "projects/demo/attestors/prod" {
		t.Fatalf("name = %q", a.Name)
	}

	wantEmail := "service-demo@gcp-sa-binaryauthorization.iam.gserviceaccount.com"
	if a.UserOwnedGrafeasNote.DelegationServiceAccountEmail != wantEmail {
		t.Fatalf("delegation email = %q, want %q", a.UserOwnedGrafeasNote.DelegationServiceAccountEmail, wantEmail)
	}

	got, err := m.GetAttestor(ctx, a.Name)
	if err != nil {
		t.Fatalf("GetAttestor: %v", err)
	}

	if !got.UpdateTime.Equal(a.UpdateTime) {
		t.Fatalf("updateTime drifted on read")
	}

	if string(got.UserOwnedGrafeasNote.PublicKeys) != `[{"id":"k1"}]` {
		t.Fatalf("publicKeys not verbatim: %s", got.UserOwnedGrafeasNote.PublicKeys)
	}

	upd, err := m.UpdateAttestor(ctx, driver.AttestorConfig{
		Name:                 a.Name,
		Description:          "changed",
		UserOwnedGrafeasNote: got.UserOwnedGrafeasNote,
	})
	if err != nil {
		t.Fatalf("UpdateAttestor: %v", err)
	}

	if upd.Description != "changed" {
		t.Fatalf("description not updated")
	}

	if err := m.DeleteAttestor(ctx, a.Name); err != nil {
		t.Fatalf("DeleteAttestor: %v", err)
	}

	if _, err := m.GetAttestor(ctx, a.Name); !cerrors.IsNotFound(err) {
		t.Fatalf("GetAttestor after delete = %v, want NotFound", err)
	}
}

func TestAttestorGuards(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Update a nonexistent attestor -> NotFound.
	_, err := m.UpdateAttestor(ctx, driver.AttestorConfig{Name: "projects/demo/attestors/ghost"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("UpdateAttestor(missing) = %v, want NotFound", err)
	}

	// Get a nonexistent attestor -> NotFound.
	if _, err := m.GetAttestor(ctx, "projects/demo/attestors/ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetAttestor(missing) = %v, want NotFound", err)
	}

	// Duplicate create -> AlreadyExists.
	cfg := driver.AttestorConfig{UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "n"}}
	if _, err := m.CreateAttestor(ctx, project, "dup", cfg); err != nil {
		t.Fatalf("first CreateAttestor: %v", err)
	}

	if _, err := m.CreateAttestor(ctx, project, "dup", cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate CreateAttestor = %v, want AlreadyExists", err)
	}
}

func TestListAttestorsScoped(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	cfg := driver.AttestorConfig{UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "n"}}
	if _, err := m.CreateAttestor(ctx, project, "a", cfg); err != nil {
		t.Fatalf("Create a: %v", err)
	}

	if _, err := m.CreateAttestor(ctx, "other", "b", cfg); err != nil {
		t.Fatalf("Create b: %v", err)
	}

	list, err := m.ListAttestors(ctx, project)
	if err != nil {
		t.Fatalf("ListAttestors: %v", err)
	}

	if len(list) != 1 || list[0].Name != "projects/demo/attestors/a" {
		t.Fatalf("list = %d attestors, want 1 (a)", len(list))
	}
}

func TestAttestorIAMPolicy(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	cfg := driver.AttestorConfig{UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "n"}}

	a, err := m.CreateAttestor(ctx, project, "iam", cfg)
	if err != nil {
		t.Fatalf("CreateAttestor: %v", err)
	}

	// getIamPolicy on an existing attestor with no policy returns an empty one.
	empty, err := m.GetIamPolicy(ctx, a.Name)
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if empty.Version != 1 {
		t.Fatalf("empty policy version = %d, want 1", empty.Version)
	}

	if _, err := m.SetIamPolicy(ctx, a.Name, driver.IAMPolicy{
		Bindings: []driver.IAMBinding{{Role: "roles/viewer", Members: []string{"user:x@y.com"}}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := m.GetIamPolicy(ctx, a.Name)
	if err != nil {
		t.Fatalf("GetIamPolicy(2): %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/viewer" {
		t.Fatalf("IAM policy not round-tripped: %+v", got.Bindings)
	}

	// IAM policy survives an attestor update.
	if _, err := m.UpdateAttestor(ctx, driver.AttestorConfig{
		Name:                 a.Name,
		UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "n"},
	}); err != nil {
		t.Fatalf("UpdateAttestor: %v", err)
	}

	after, _ := m.GetIamPolicy(ctx, a.Name)
	if len(after.Bindings) != 1 {
		t.Fatalf("IAM policy lost across attestor update: %+v", after.Bindings)
	}
}

func TestIAMOnMissingAttestor(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.GetIamPolicy(ctx, "projects/demo/attestors/ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetIamPolicy(missing) = %v, want NotFound", err)
	}
}
