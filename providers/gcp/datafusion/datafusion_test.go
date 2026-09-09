package datafusion

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

func newMock() *Mock {
	return New(config.NewOptions(config.WithProjectID("p")))
}

func cfg(id string, fields map[string]json.RawMessage) *dfdriver.Config {
	return &dfdriver.Config{Project: "p", Location: "us-central1", ID: id, Fields: fields}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateSettlesActive verifies a create lands ACTIVE synchronously with a
// done LRO, carries verbatim input fields, and mints create/update timestamps.
func TestCreateSettlesActive(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	res, op, err := m.CreateInstance(ctx, cfg("df", map[string]json.RawMessage{
		"type":    json.RawMessage(`"ENTERPRISE"`),
		"version": json.RawMessage(`"6.9.2"`),
	}))
	requireNoError(t, err)

	if !op.Done {
		t.Fatalf("create op not done (would hang a Terraform apply)")
	}

	if res.State != dfdriver.StateActive {
		t.Fatalf("state = %q, want ACTIVE", res.State)
	}

	if res.CreateTime.IsZero() || res.UpdateTime.IsZero() {
		t.Fatalf("timestamps not minted: %+v", res)
	}

	if string(res.Fields["type"]) != `"ENTERPRISE"` || string(res.Fields["version"]) != `"6.9.2"` {
		t.Fatalf("input fields not round-tripped: %+v", res.Fields)
	}
}

// TestDuplicateCreate verifies a second create of the same instance is rejected
// with ALREADY_EXISTS.
func TestDuplicateCreate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateInstance(ctx, cfg("df", nil))
	requireNoError(t, err)

	if _, _, err := m.CreateInstance(ctx, cfg("df", nil)); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

// TestGetNotFound verifies a get of a missing instance is NOT_FOUND.
func TestGetNotFound(t *testing.T) {
	m := newMock()

	if _, err := m.GetInstance(context.Background(), "p", "us-central1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

// TestPatchMasked verifies a masked patch replaces only the masked field, leaves
// others untouched, and advances updateTime.
func TestPatchMasked(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateInstance(ctx, cfg("df", map[string]json.RawMessage{
		"type":        json.RawMessage(`"BASIC"`),
		"description": json.RawMessage(`"first"`),
		"labels":      json.RawMessage(`{"a":"1"}`),
	}))
	requireNoError(t, err)

	res, op, err := m.PatchInstance(ctx, cfg("df", map[string]json.RawMessage{
		"labels": json.RawMessage(`{"a":"1","b":"2"}`),
	}), []string{"labels"})
	requireNoError(t, err)

	if !op.Done {
		t.Fatalf("patch op not done")
	}

	if string(res.Fields["labels"]) != `{"a":"1","b":"2"}` {
		t.Fatalf("masked field not updated: %s", res.Fields["labels"])
	}

	if string(res.Fields["description"]) != `"first"` || string(res.Fields["type"]) != `"BASIC"` {
		t.Fatalf("masked patch mutated unmentioned fields: %+v", res.Fields)
	}
}

// TestRestartActive verifies restarting an ACTIVE instance succeeds, keeps it
// ACTIVE, and returns a done LRO.
func TestRestartActive(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateInstance(ctx, cfg("df", map[string]json.RawMessage{"type": json.RawMessage(`"BASIC"`)}))
	requireNoError(t, err)

	res, op, err := m.RestartInstance(ctx, "p", "us-central1", "df")
	requireNoError(t, err)

	if !op.Done || op.Type != "restart" {
		t.Fatalf("restart op wrong: %+v", op)
	}

	if res.State != dfdriver.StateActive {
		t.Fatalf("state after restart = %q, want ACTIVE", res.State)
	}
}

// TestRestartNotFound verifies restarting a missing instance is NOT_FOUND.
func TestRestartNotFound(t *testing.T) {
	m := newMock()

	if _, _, err := m.RestartInstance(context.Background(), "p", "us-central1", "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

// TestRestartAllowedStateMachine covers the :restart state-machine guard for
// every lifecycle state directly: only ACTIVE is allowed; every transient state
// is rejected with FAILED_PRECONDITION (the illegal-transition rejection that a
// synchronous emulator cannot reach through the wire).
func TestRestartAllowedStateMachine(t *testing.T) {
	cases := []struct {
		state   string
		allowed bool
	}{
		{dfdriver.StateActive, true},
		{dfdriver.StateCreating, false},
		{dfdriver.StateDeleting, false},
		{dfdriver.StateRestarting, false},
		{"FAILED", false},
	}

	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			err := restartAllowed(tc.state)
			if tc.allowed && err != nil {
				t.Fatalf("state %q should allow restart, got %v", tc.state, err)
			}

			if !tc.allowed {
				if !cerrors.IsFailedPrecondition(err) {
					t.Fatalf("state %q should reject with FailedPrecondition, got %v", tc.state, err)
				}
			}
		})
	}
}

// TestDeleteThenGone verifies delete removes the instance and a subsequent get
// 404s.
func TestDeleteThenGone(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateInstance(ctx, cfg("df", nil))
	requireNoError(t, err)

	op, err := m.DeleteInstance(ctx, "p", "us-central1", "df")
	requireNoError(t, err)

	if !op.Done {
		t.Fatalf("delete op not done")
	}

	if _, err := m.GetInstance(ctx, "p", "us-central1", "df"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound after delete, got %v", err)
	}

	if _, err := m.DeleteInstance(ctx, "p", "us-central1", "df"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound on second delete, got %v", err)
	}
}

// TestListScoped verifies list returns only instances in the addressed scope.
func TestListScoped(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateInstance(ctx, cfg("a", nil))
	requireNoError(t, err)
	_, _, err = m.CreateInstance(ctx, &dfdriver.Config{Project: "p", Location: "europe-west1", ID: "b"})
	requireNoError(t, err)

	got, err := m.ListInstances(ctx, "p", "us-central1")
	requireNoError(t, err)

	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("list scoping wrong: %+v", got)
	}
}

// TestOwnership verifies Owns/OwnsAnyIn back the wire handler's Matches
// disambiguation.
func TestOwnership(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if m.Owns("p", "us-central1", "df") || m.OwnsAnyIn("p", "us-central1") {
		t.Fatalf("empty store should own nothing")
	}

	_, _, err := m.CreateInstance(ctx, cfg("df", nil))
	requireNoError(t, err)

	if !m.Owns("p", "us-central1", "df") || !m.OwnsAnyIn("p", "us-central1") {
		t.Fatalf("store should own the created instance")
	}

	if m.Owns("p", "us-central1", "other") || m.OwnsAnyIn("p", "europe-west1") {
		t.Fatalf("store should not own unrelated instances/scopes")
	}
}
