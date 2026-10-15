package resourceiam

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const res = "projects/p/instances/i"

func viewer(member string) []Binding {
	return []Binding{{Role: "roles/viewer", Members: []string{member}}}
}

func TestGetUnsetPolicy(t *testing.T) {
	m := New()

	got := m.Get(res)
	if got.Version != 1 || got.Etag != InitialEtag() || len(got.Bindings) != 0 {
		t.Fatalf("Get unset = %+v, want version 1 with etag %q", got, InitialEtag())
	}

	if again := m.Get(res); again.Etag != got.Etag {
		t.Fatalf("unset etag changed between reads: %q then %q", got.Etag, again.Etag)
	}
}

func TestSetEtagContract(t *testing.T) {
	m := New()

	first, err := m.Set(res, Policy{Bindings: viewer("user:a")}, "")
	if err != nil {
		t.Fatalf("blind set: %v", err)
	}

	if first.Etag == InitialEtag() {
		t.Fatalf("blind set kept the initial etag %q", first.Etag)
	}

	second, err := m.Set(res, Policy{Bindings: viewer("user:b"), Etag: first.Etag}, "")
	if err != nil {
		t.Fatalf("set with current etag: %v", err)
	}

	if second.Etag == first.Etag {
		t.Fatalf("set did not mint a new etag: %q", second.Etag)
	}

	if _, err := m.Set(res, Policy{Bindings: viewer("user:c"), Etag: first.Etag}, ""); !errors.Is(err, ErrAborted) {
		t.Fatalf("stale etag: err = %v, want ErrAborted", err)
	}

	if got := m.Get(res); got.Bindings[0].Members[0] != "user:b" {
		t.Fatalf("stale write landed: %+v", got)
	}
}

func TestSetConditionNeedsVersion3(t *testing.T) {
	cond := []Binding{{Role: "roles/viewer", Members: []string{"user:a"}, Condition: &Expr{Expression: "true", Title: "t"}}}

	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"unset version", 0, true},
		{"version 1", 1, true},
		{"version 3", 3, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New().Set(res, Policy{Version: tc.version, Bindings: cond}, "")
			if tc.wantErr {
				if !cerrors.IsInvalidArgument(err) {
					t.Fatalf("err = %v, want InvalidArgument", err)
				}

				return
			}

			if err != nil || got.Version != 3 || got.Bindings[0].Condition.Title != "t" {
				t.Fatalf("got %+v, %v; want a version 3 conditional policy", got, err)
			}
		})
	}
}

func TestSetUpdateMask(t *testing.T) {
	audit := []AuditConfig{{Service: "allServices", AuditLogConfigs: []AuditLogConfig{{LogType: "DATA_READ"}}}}

	tests := []struct {
		name      string
		mask      string
		wantAudit int
	}{
		{"default mask keeps audit configs", "", 1},
		{"bindings mask keeps audit configs", "bindings", 1},
		{"audit mask replaces them", "bindings,auditConfigs", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			if _, err := m.Set(res, Policy{Bindings: viewer("user:a"), AuditConfigs: audit}, "bindings,auditConfigs"); err != nil {
				t.Fatal(err)
			}

			got, err := m.Set(res, Policy{Bindings: viewer("user:b")}, tc.mask)
			if err != nil {
				t.Fatal(err)
			}

			if len(got.AuditConfigs) != tc.wantAudit || got.Bindings[0].Members[0] != "user:b" {
				t.Fatalf("got %+v, want %d audit configs and user:b", got, tc.wantAudit)
			}
		})
	}
}

func TestSetDropsEmptyBindings(t *testing.T) {
	got, err := New().Set(res, Policy{Bindings: []Binding{{Role: "roles/viewer"}}}, "")
	if err != nil || len(got.Bindings) != 0 {
		t.Fatalf("got %+v, %v; want the memberless binding dropped", got, err)
	}
}

func TestDeleteClearsResourceAndChildren(t *testing.T) {
	m := New()

	for _, name := range []string{res, res + "/databases/d", res + "2"} {
		if _, err := m.Set(name, Policy{Bindings: viewer("user:a")}, ""); err != nil {
			t.Fatal(err)
		}
	}

	m.Delete(res)

	for name, wantSet := range map[string]bool{res: false, res + "/databases/d": false, res + "2": true} {
		if got := len(m.Get(name).Bindings) > 0; got != wantSet {
			t.Errorf("%s: policy present = %v, want %v", name, got, wantSet)
		}
	}
}

func TestGetReturnsCopy(t *testing.T) {
	m := New()
	if _, err := m.Set(res, Policy{Bindings: viewer("user:a")}, ""); err != nil {
		t.Fatal(err)
	}

	got := m.Get(res)
	got.Bindings[0].Members[0] = "user:mutated"

	if m.Get(res).Bindings[0].Members[0] != "user:a" {
		t.Fatal("mutating a returned policy changed the store")
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := New()

	set, err := m.Set(res, Policy{Bindings: viewer("user:a")}, "")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored := New()
	if err := restored.Restore(ctx, raw); err != nil {
		t.Fatal(err)
	}

	got := restored.Get(res)
	if got.Etag != set.Etag || got.Bindings[0].Members[0] != "user:a" {
		t.Fatalf("restored %+v, want %+v", got, set)
	}

	if _, err := restored.Set(res, Policy{Etag: InitialEtag()}, ""); !errors.Is(err, ErrAborted) {
		t.Fatalf("restored store accepted a stale etag: %v", err)
	}
}
