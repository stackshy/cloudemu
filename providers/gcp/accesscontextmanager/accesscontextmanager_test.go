package accesscontextmanager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func policyFields(parent, title string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"parent": raw(`"` + parent + `"`),
		"title":  raw(`"` + title + `"`),
	}
}

func TestPolicyCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &acmdriver.PolicyConfig{Fields: policyFields("organizations/123", "prod")}

	p, op, err := m.CreatePolicy(ctx, cfg)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if !op.Done || op.Type != "create" || op.Kind != "policy" {
		t.Fatalf("op = %+v", op)
	}

	if p.Number == "" || p.Etag == "" || p.CreateTime.IsZero() || p.UpdateTime.IsZero() {
		t.Fatalf("computed fields missing: %+v", p)
	}

	// Number is deterministic from parent+title.
	if got := policyNumber("organizations/123", "prod"); got != p.Number {
		t.Fatalf("number not deterministic: %s vs %s", got, p.Number)
	}

	got, err := m.GetPolicy(ctx, p.Number)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}

	if got.Etag != p.Etag || got.Number != p.Number {
		t.Fatalf("read drift: %+v vs %+v", got, p)
	}

	// Duplicate create -> AlreadyExists.
	if _, _, err := m.CreatePolicy(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// Deep-clone isolation.
	got.Fields["title"] = raw(`"tampered"`)

	again, _ := m.GetPolicy(ctx, p.Number)
	if string(again.Fields["title"]) != `"prod"` {
		t.Fatalf("stored resource aliased: %s", again.Fields["title"])
	}

	patched, _, err := m.PatchPolicy(ctx, p.Number, &acmdriver.PolicyConfig{
		Fields: map[string]json.RawMessage{"title": raw(`"prod-v2"`)},
	}, []string{"title"})
	if err != nil {
		t.Fatalf("PatchPolicy: %v", err)
	}

	if string(patched.Fields["title"]) != `"prod-v2"` {
		t.Fatalf("patch not applied: %s", patched.Fields["title"])
	}

	if patched.Etag == p.Etag {
		t.Fatalf("etag did not change on patch")
	}

	if _, err := m.DeletePolicy(ctx, p.Number); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}

	if _, err := m.GetPolicy(ctx, p.Number); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestChildCRUDAndNotFoundGuard(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// Create under a nonexistent policy -> NOT_FOUND.
	if _, _, err := m.CreateAccessLevel(ctx, &acmdriver.ChildConfig{
		PolicyNumber: "999", ID: "lvl", Fields: map[string]json.RawMessage{"title": raw(`"l"`)},
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("create under missing policy err = %v, want NotFound", err)
	}

	p, _, err := m.CreatePolicy(ctx, &acmdriver.PolicyConfig{Fields: policyFields("organizations/9", "t")})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	basic := raw(`{"conditions":[{"ipSubnetworks":["10.0.0.0/8"]}]}`)

	lvl, op, err := m.CreateAccessLevel(ctx, &acmdriver.ChildConfig{
		PolicyNumber: p.Number, ID: "lvl",
		Fields: map[string]json.RawMessage{"title": raw(`"corp"`), "basic": basic},
	})
	if err != nil {
		t.Fatalf("CreateAccessLevel: %v", err)
	}

	if op.Kind != "accessLevel" || lvl.Etag == "" {
		t.Fatalf("level op/etag wrong: %+v %s", op, lvl.Etag)
	}

	// basic block round-trips verbatim.
	gotLvl, _ := m.GetAccessLevel(ctx, p.Number, "lvl")
	if string(gotLvl.Fields["basic"]) != string(basic) {
		t.Fatalf("basic block drift: %s", gotLvl.Fields["basic"])
	}

	// Duplicate child -> AlreadyExists.
	if _, _, err := m.CreateAccessLevel(ctx, &acmdriver.ChildConfig{
		PolicyNumber: p.Number, ID: "lvl", Fields: map[string]json.RawMessage{"title": raw(`"x"`)},
	}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate child err = %v, want AlreadyExists", err)
	}

	status := raw(`{"resources":["projects/1"],"restrictedServices":["storage.googleapis.com"]}`)

	if _, _, err := m.CreateServicePerimeter(ctx, &acmdriver.ChildConfig{
		PolicyNumber: p.Number, ID: "peri", Fields: map[string]json.RawMessage{"title": raw(`"pp"`), "status": status},
	}); err != nil {
		t.Fatalf("CreateServicePerimeter: %v", err)
	}

	gotPeri, _ := m.GetServicePerimeter(ctx, p.Number, "peri")
	if string(gotPeri.Fields["status"]) != string(status) {
		t.Fatalf("status block drift: %s", gotPeri.Fields["status"])
	}

	// Cascade delete: deleting the policy removes its children.
	if _, err := m.DeletePolicy(ctx, p.Number); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}

	if _, err := m.GetAccessLevel(ctx, p.Number, "lvl"); !cerrors.IsNotFound(err) {
		t.Fatalf("child survived policy delete: %v", err)
	}
}

func TestListScopedByParentAndPolicy(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	pa, _, _ := m.CreatePolicy(ctx, &acmdriver.PolicyConfig{Fields: policyFields("organizations/1", "a")})
	m.CreatePolicy(ctx, &acmdriver.PolicyConfig{Fields: policyFields("organizations/2", "b")})

	got, err := m.ListPolicies(ctx, "organizations/1")
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}

	if len(got) != 1 || got[0].Number != pa.Number {
		t.Fatalf("parent scope wrong: %+v", got)
	}

	m.CreateAccessLevel(ctx, &acmdriver.ChildConfig{PolicyNumber: pa.Number, ID: "b", Fields: nil})
	m.CreateAccessLevel(ctx, &acmdriver.ChildConfig{PolicyNumber: pa.Number, ID: "a", Fields: nil})

	levels, _ := m.ListAccessLevels(ctx, pa.Number)
	if len(levels) != 2 || levels[0].ID != "a" || levels[1].ID != "b" {
		t.Fatalf("child list scope/order wrong: %+v", levels)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	p, _, _ := m.CreatePolicy(ctx, &acmdriver.PolicyConfig{Fields: policyFields("organizations/1", "t")})
	m.CreateServicePerimeter(ctx, &acmdriver.ChildConfig{
		PolicyNumber: p.Number, ID: "peri",
		Fields: map[string]json.RawMessage{"status": raw(`{"restrictedServices":["s.googleapis.com"]}`)},
	})

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotP, err := restored.GetPolicy(ctx, p.Number)
	if err != nil {
		t.Fatalf("GetPolicy after restore: %v", err)
	}

	if gotP.Etag != p.Etag {
		t.Fatalf("etag drift after restore: %s vs %s", gotP.Etag, p.Etag)
	}

	gotPeri, err := restored.GetServicePerimeter(ctx, p.Number, "peri")
	if err != nil {
		t.Fatalf("GetServicePerimeter after restore: %v", err)
	}

	if string(gotPeri.Fields["status"]) != `{"restrictedServices":["s.googleapis.com"]}` {
		t.Fatalf("restored status wrong: %s", gotPeri.Fields["status"])
	}
}
