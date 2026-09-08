package binaryauthorization_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/binaryauthorization"
	"github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := binaryauthorization.New(config.NewOptions())

	if _, err := src.UpdatePolicy(ctx, project, &driver.PolicyConfig{
		Description:          "prod",
		DefaultAdmissionRule: json.RawMessage(`{"evaluationMode":"ALWAYS_DENY"}`),
	}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	a, err := src.CreateAttestor(ctx, project, "prod", driver.AttestorConfig{
		UserOwnedGrafeasNote: &driver.UserOwnedGrafeasNote{NoteReference: "projects/demo/notes/n"},
	})
	if err != nil {
		t.Fatalf("CreateAttestor: %v", err)
	}

	if _, err := src.SetIamPolicy(ctx, a.Name, driver.IAMPolicy{
		Bindings: []driver.IAMBinding{{Role: "roles/viewer", Members: []string{"user:x@y.com"}}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := binaryauthorization.New(config.NewOptions())
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	p, err := dst.GetPolicy(ctx, project)
	if err != nil {
		t.Fatalf("GetPolicy after restore: %v", err)
	}

	if p.Description != "prod" || string(p.DefaultAdmissionRule) != `{"evaluationMode":"ALWAYS_DENY"}` {
		t.Fatalf("policy not restored faithfully: %+v", p)
	}

	got, err := dst.GetAttestor(ctx, a.Name)
	if err != nil {
		t.Fatalf("GetAttestor after restore: %v", err)
	}

	if got.UserOwnedGrafeasNote.NoteReference != "projects/demo/notes/n" {
		t.Fatalf("attestor not restored: %+v", got)
	}

	pol, err := dst.GetIamPolicy(ctx, a.Name)
	if err != nil {
		t.Fatalf("GetIamPolicy after restore: %v", err)
	}

	if len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/viewer" {
		t.Fatalf("IAM policy not restored: %+v", pol)
	}
}
