package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestProjectScopedRoles(t *testing.T) {
	m := newTestMock()
	bg := context.Background()
	pb := projectctx.WithProject(bg, "p-b")

	if _, err := m.CreateRole(bg, driver.RoleConfig{Name: "r", AssumeRolePolicyDoc: "a"}); err != nil {
		t.Fatalf("create default: %v", err)
	}

	info, err := m.CreateRole(pb, driver.RoleConfig{Name: "r", AssumeRolePolicyDoc: "b"})
	if err != nil {
		t.Fatalf("create p-b with the same id: %v", err)
	}

	if !strings.Contains(info.ARN, "p-b") {
		t.Fatalf("p-b role ARN = %q, want it under p-b", info.ARN)
	}

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"unstamped is the default project", bg, "a"},
		{"other project", pb, "b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.GetRole(tc.ctx, "r")
			if err != nil || got.AssumeRolePolicyDoc != tc.want {
				t.Fatalf("GetRole = %+v, %v; want doc %q", got, err, tc.want)
			}

			list, err := m.ListRoles(tc.ctx)
			if err != nil || len(list) != 1 {
				t.Fatalf("ListRoles = %+v, %v; want one role", list, err)
			}
		})
	}

	if list, _ := m.ListRoles(projectctx.AllProjects(bg)); len(list) != 2 {
		t.Fatalf("AllProjects ListRoles = %d roles, want 2", len(list))
	}

	if err := m.DeleteRole(pb, "r"); err != nil {
		t.Fatalf("delete p-b: %v", err)
	}

	if _, err := m.GetRole(bg, "r"); err != nil {
		t.Fatalf("default role after p-b delete: %v", err)
	}
}

// TestRestoreAdoptsLegacyRoles loads a snapshot taken before project scoping.
// A wire-created role kept its project in Path and lands back there; a
// typed-API role ("/" path) lands in the default project.
func TestRestoreAdoptsLegacyRoles(t *testing.T) {
	var buf bytes.Buffer

	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)

	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	legacy := json.RawMessage(`{
		"roles":{"wire":{"Name":"wire","Path":"p-b"},"typed":{"Name":"typed","Path":"/"}},
		"rolePolicies":{"wire":{"pol-1":true}}}`)

	m := newTestMock()
	bg := context.Background()
	pb := projectctx.WithProject(bg, "p-b")

	if err := m.Restore(bg, legacy); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := m.GetRole(pb, "wire"); err != nil {
		t.Fatalf("wire role under p-b: %v", err)
	}

	if _, err := m.GetRole(bg, "wire"); err == nil {
		t.Fatal("wire role visible under the default project")
	}

	if _, err := m.GetRole(bg, "typed"); err != nil {
		t.Fatalf("typed role under the default project: %v", err)
	}

	if pols, err := m.ListAttachedRolePolicies(pb, "wire"); err != nil || len(pols) != 1 {
		t.Fatalf("wire role attachments = %v, %v; want the restored one", pols, err)
	}

	out := buf.String()
	if strings.Count(out, "gcp/iam: adopted 1 legacy") != 1 {
		t.Fatalf("restore log = %q, want one adopted-1 warning", out)
	}
}
