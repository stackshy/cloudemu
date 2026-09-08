package binaryauthorization_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	binauthz "google.golang.org/api/binaryauthorization/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	testProject = "projects/demo"
	policyName  = testProject + "/policy"
)

func newBinauthzService(t *testing.T) *binauthz.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{BinaryAuthorization: cloud.BinaryAuthorization})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := binauthz.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("binaryauthorization.NewService: %v", err)
	}

	return svc
}

// TestSDKPolicyDefaultAndStability confirms a project that never set a policy
// reads back a seeded ALWAYS_ALLOW default, and that name + updateTime are
// byte-stable across repeated reads (no clock read on GET).
func TestSDKPolicyDefaultAndStability(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()

	got, err := svc.Projects.GetPolicy(policyName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if got.Name != policyName {
		t.Fatalf("name = %q, want %q", got.Name, policyName)
	}

	if got.DefaultAdmissionRule == nil || got.DefaultAdmissionRule.EvaluationMode != "ALWAYS_ALLOW" {
		t.Fatalf("default rule = %+v, want ALWAYS_ALLOW", got.DefaultAdmissionRule)
	}

	if got.GlobalPolicyEvaluationMode != "ENABLE" {
		t.Fatalf("globalPolicyEvaluationMode = %q, want ENABLE", got.GlobalPolicyEvaluationMode)
	}

	if got.UpdateTime == "" {
		t.Fatalf("updateTime empty on seeded default")
	}

	again, err := svc.Projects.GetPolicy(policyName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetPolicy(2): %v", err)
	}

	if again.UpdateTime != got.UpdateTime || again.Etag != got.Etag {
		t.Fatalf("policy not byte-stable across reads: updateTime %q vs %q", again.UpdateTime, got.UpdateTime)
	}
}

// TestSDKPolicyUpdateRoundTrip drives a full-replace update with a default rule,
// a cluster rule and a whitelist pattern, and confirms every block round-trips.
func TestSDKPolicyUpdateRoundTrip(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()

	want := &binauthz.Policy{
		Description:                "prod policy",
		GlobalPolicyEvaluationMode: "DISABLE",
		AdmissionWhitelistPatterns: []*binauthz.AdmissionWhitelistPattern{
			{NamePattern: "gcr.io/my-project/*"},
		},
		DefaultAdmissionRule: &binauthz.AdmissionRule{
			EvaluationMode:  "REQUIRE_ATTESTATION",
			EnforcementMode: "ENFORCED_BLOCK_AND_AUDIT_LOG",
			RequireAttestationsBy: []string{
				testProject + "/attestors/prod",
			},
		},
		ClusterAdmissionRules: map[string]binauthz.AdmissionRule{
			"us-central1-a.prod": {
				EvaluationMode:  "REQUIRE_ATTESTATION",
				EnforcementMode: "DRYRUN_AUDIT_LOG_ONLY",
				RequireAttestationsBy: []string{
					testProject + "/attestors/prod",
				},
			},
		},
	}

	updated, err := svc.Projects.UpdatePolicy(policyName, want).Context(ctx).Do()
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	assertPolicyMatches(t, updated, want)

	got, err := svc.Projects.GetPolicy(policyName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	assertPolicyMatches(t, got, want)

	if got.UpdateTime != updated.UpdateTime {
		t.Fatalf("updateTime drifted on read: %q vs %q", got.UpdateTime, updated.UpdateTime)
	}
}

func assertPolicyMatches(t *testing.T, got, want *binauthz.Policy) {
	t.Helper()

	if got.Description != want.Description {
		t.Fatalf("description = %q, want %q", got.Description, want.Description)
	}

	if got.GlobalPolicyEvaluationMode != want.GlobalPolicyEvaluationMode {
		t.Fatalf("globalPolicyEvaluationMode = %q, want %q", got.GlobalPolicyEvaluationMode, want.GlobalPolicyEvaluationMode)
	}

	if got.DefaultAdmissionRule == nil || got.DefaultAdmissionRule.EvaluationMode != want.DefaultAdmissionRule.EvaluationMode {
		t.Fatalf("defaultAdmissionRule = %+v, want %+v", got.DefaultAdmissionRule, want.DefaultAdmissionRule)
	}

	if len(got.DefaultAdmissionRule.RequireAttestationsBy) != 1 {
		t.Fatalf("requireAttestationsBy = %+v, want 1 entry", got.DefaultAdmissionRule.RequireAttestationsBy)
	}

	rule, ok := got.ClusterAdmissionRules["us-central1-a.prod"]
	if !ok || rule.EnforcementMode != "DRYRUN_AUDIT_LOG_ONLY" {
		t.Fatalf("clusterAdmissionRules = %+v, want a DRYRUN rule", got.ClusterAdmissionRules)
	}

	if len(got.AdmissionWhitelistPatterns) != 1 || got.AdmissionWhitelistPatterns[0].NamePattern != "gcr.io/my-project/*" {
		t.Fatalf("admissionWhitelistPatterns = %+v", got.AdmissionWhitelistPatterns)
	}
}

// TestSDKAttestorLifecycle covers create/get/list/update and the computed
// delegationServiceAccountEmail + byte-stable name/updateTime.
func TestSDKAttestorLifecycle(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()
	name := testProject + "/attestors/prod"

	created, err := svc.Projects.Attestors.Create(testProject, &binauthz.Attestor{
		Description: "prod attestor",
		UserOwnedGrafeasNote: &binauthz.UserOwnedGrafeasNote{
			NoteReference: "projects/demo/notes/prod-note",
			PublicKeys: []*binauthz.AttestorPublicKey{
				{Id: "key-1", AsciiArmoredPgpPublicKey: "-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n-----END-----"},
			},
		},
	}).AttestorId("prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.Name != name {
		t.Fatalf("name = %q, want %q", created.Name, name)
	}

	note := created.UserOwnedGrafeasNote
	if note == nil || note.NoteReference != "projects/demo/notes/prod-note" {
		t.Fatalf("note not round-tripped: %+v", note)
	}

	if note.DelegationServiceAccountEmail == "" {
		t.Fatalf("delegationServiceAccountEmail not computed")
	}

	if len(note.PublicKeys) != 1 || note.PublicKeys[0].Id != "key-1" {
		t.Fatalf("publicKeys not round-tripped: %+v", note.PublicKeys)
	}

	got, err := svc.Projects.Attestors.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.UpdateTime != created.UpdateTime {
		t.Fatalf("updateTime drifted on read: %q vs %q", got.UpdateTime, created.UpdateTime)
	}

	if got.UserOwnedGrafeasNote.DelegationServiceAccountEmail != note.DelegationServiceAccountEmail {
		t.Fatalf("delegationServiceAccountEmail not stable across read")
	}

	list, err := svc.Projects.Attestors.List(testProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Attestors) != 1 || list.Attestors[0].Name != name {
		t.Fatalf("list = %d attestors, want 1 (prod)", len(list.Attestors))
	}

	updated, err := svc.Projects.Attestors.Update(name, &binauthz.Attestor{
		Description:          "updated desc",
		UserOwnedGrafeasNote: got.UserOwnedGrafeasNote,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Description != "updated desc" {
		t.Fatalf("description not updated: %q", updated.Description)
	}
}

func TestSDKAttestorDeleteThenGet404(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()
	name := testProject + "/attestors/ephemeral"

	if _, err := svc.Projects.Attestors.Create(testProject, &binauthz.Attestor{
		UserOwnedGrafeasNote: &binauthz.UserOwnedGrafeasNote{NoteReference: "projects/demo/notes/n"},
	}).AttestorId("ephemeral").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Attestors.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := svc.Projects.Attestors.Get(name).Context(ctx).Do()
	assertGoogleErr(t, err, 404)
}

func TestSDKAttestorUpdateMissing404(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()

	_, err := svc.Projects.Attestors.Update(testProject+"/attestors/ghost", &binauthz.Attestor{
		Description: "nope",
	}).Context(ctx).Do()
	assertGoogleErr(t, err, 404)
}

func TestSDKAttestorDuplicateCreate409(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()

	mk := func() error {
		_, err := svc.Projects.Attestors.Create(testProject, &binauthz.Attestor{
			UserOwnedGrafeasNote: &binauthz.UserOwnedGrafeasNote{NoteReference: "projects/demo/notes/n"},
		}).AttestorId("dup").Context(ctx).Do()

		return err
	}

	if err := mk(); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	assertGoogleErr(t, mk(), 409)
}

func TestSDKAttestorIAMPolicy(t *testing.T) {
	svc := newBinauthzService(t)
	ctx := context.Background()
	name := testProject + "/attestors/iam"

	if _, err := svc.Projects.Attestors.Create(testProject, &binauthz.Attestor{
		UserOwnedGrafeasNote: &binauthz.UserOwnedGrafeasNote{NoteReference: "projects/demo/notes/n"},
	}).AttestorId("iam").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	set, err := svc.Projects.Attestors.SetIamPolicy(name, &binauthz.SetIamPolicyRequest{
		Policy: &binauthz.IamPolicy{
			Bindings: []*binauthz.Binding{{
				Role:    "roles/binaryauthorization.attestorsViewer",
				Members: []string{"user:a@b.com"},
			}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == "" {
		t.Fatalf("setIamPolicy returned no etag")
	}

	got, err := svc.Projects.Attestors.GetIamPolicy(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/binaryauthorization.attestorsViewer" {
		t.Fatalf("policy round-trip: %+v", got.Bindings)
	}

	test, err := svc.Projects.Attestors.TestIamPermissions(name, &binauthz.TestIamPermissionsRequest{
		Permissions: []string{"binaryauthorization.attestors.get"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(test.Permissions) != 1 || test.Permissions[0] != "binaryauthorization.attestors.get" {
		t.Fatalf("testIamPermissions = %+v", test.Permissions)
	}
}

func assertGoogleErr(t *testing.T, err error, wantCode int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %d, got nil", wantCode)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error %v is not a *googleapi.Error", err)
	}

	if gerr.Code != wantCode {
		t.Fatalf("error code = %d, want %d", gerr.Code, wantCode)
	}
}
