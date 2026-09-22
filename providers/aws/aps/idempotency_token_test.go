package aps_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

func TestCreateWorkspaceClientToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateWorkspaceInput{Alias: "prod", ClientToken: "tok-1"}

	first, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)

	retry, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)

	if retry.WorkspaceID != first.WorkspaceID || retry.Arn != first.Arn {
		t.Fatalf("same-token retry = %s, want original %s", retry.WorkspaceID, first.WorkspaceID)
	}

	in.ClientToken = "tok-2"

	other, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)

	if other.WorkspaceID == first.WorkspaceID {
		t.Fatalf("different token must mint a new workspace, got %s again", other.WorkspaceID)
	}

	list, _, err := m.ListWorkspaces(ctx, "", driver.Page{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("workspace count = %d, want 2 (no duplicate on retry)", len(list))
	}
}

func TestCreateRuleGroupsNamespaceClientToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	ws := createWorkspace(t, m, "prod")

	in := &driver.RuleGroupsNamespaceInput{WorkspaceID: ws.WorkspaceID, Name: "rg", Data: "ZGF0YQ==", ClientToken: "tok-1"}

	first, err := m.CreateRuleGroupsNamespace(ctx, in)
	requireNoError(t, err)

	// A retry resends the same name; it must replay the original, not conflict.
	retry, err := m.CreateRuleGroupsNamespace(ctx, in)
	requireNoError(t, err)

	if retry.Arn != first.Arn {
		t.Fatalf("same-token retry arn = %s, want %s", retry.Arn, first.Arn)
	}

	// The same name under a different token is a genuine conflict.
	in.ClientToken = "tok-2"
	if _, err = m.CreateRuleGroupsNamespace(ctx, in); err == nil {
		t.Fatal("same name with a different token must conflict")
	}

	// A different namespace name under a new token creates a new namespace.
	in.Name = "rg-2"

	other, err := m.CreateRuleGroupsNamespace(ctx, in)
	requireNoError(t, err)

	if other.Arn == first.Arn {
		t.Fatalf("different token + name must mint a new namespace, got %s again", other.Arn)
	}

	list, _, err := m.ListRuleGroupsNamespaces(ctx, ws.WorkspaceID, "", driver.Page{})
	requireNoError(t, err)

	if len(list) != 2 {
		t.Fatalf("namespace count = %d, want 2", len(list))
	}
}

func TestCreateRuleGroupsNamespaceTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	ws := createWorkspace(t, m, "prod")

	in := &driver.RuleGroupsNamespaceInput{WorkspaceID: ws.WorkspaceID, Name: "rg", Data: "ZGF0YQ==", ClientToken: "tok-1"}

	_, err := m.CreateRuleGroupsNamespace(ctx, in)
	requireNoError(t, err)
	requireNoError(t, m.DeleteRuleGroupsNamespace(ctx, ws.WorkspaceID, "rg"))

	// The namespace the token minted is gone, so the token must not replay a ghost.
	if _, err = m.CreateRuleGroupsNamespace(ctx, in); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}

	if _, err = m.DescribeRuleGroupsNamespace(ctx, ws.WorkspaceID, "rg"); err != nil {
		t.Fatalf("namespace should exist after recreate: %v", err)
	}
}

func TestCreateWorkspaceTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateWorkspaceInput{Alias: "prod", ClientToken: "g1"}

	first, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)
	requireNoError(t, m.DeleteWorkspace(ctx, first.WorkspaceID))

	again, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)

	if again.WorkspaceID == first.WorkspaceID {
		t.Fatalf("same-token create after delete replayed the deleted workspace %s", first.WorkspaceID)
	}

	if _, err = m.DescribeWorkspace(ctx, again.WorkspaceID); err != nil {
		t.Fatalf("workspace returned after delete must exist: %v", err)
	}
}

func TestCreateWorkspaceTokenAfterUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateWorkspaceInput{Alias: "prod", ClientToken: "g1"}

	first, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)
	requireNoError(t, m.UpdateWorkspaceAlias(ctx, first.WorkspaceID, "renamed"))

	retry, err := m.CreateWorkspace(ctx, in)
	requireNoError(t, err)

	if retry.WorkspaceID != first.WorkspaceID {
		t.Fatalf("same-token retry = %s, want %s", retry.WorkspaceID, first.WorkspaceID)
	}

	if retry.Alias != "renamed" {
		t.Fatalf("replay alias = %q, want the live alias %q", retry.Alias, "renamed")
	}
}

func TestCreateWorkspaceTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	const n = 20

	ids := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			ws, err := m.CreateWorkspace(ctx, &driver.CreateWorkspaceInput{Alias: "prod", ClientToken: "burst"})
			if err == nil {
				ids[i] = ws.WorkspaceID
			}

			errs[i] = err
		}()
	}

	close(start)
	wg.Wait()

	for i := range n {
		requireNoError(t, errs[i])

		if ids[i] != ids[0] {
			t.Fatalf("call %d returned %s, want the single workspace %s", i, ids[i], ids[0])
		}
	}

	list, _, err := m.ListWorkspaces(ctx, "", driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 {
		t.Fatalf("workspace count = %d, want exactly 1 for one token", len(list))
	}
}

func TestCreateRuleGroupsNamespaceTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	ws := createWorkspace(t, m, "prod")

	const n = 20

	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			_, errs[i] = m.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
				WorkspaceID: ws.WorkspaceID, Name: "rg", Data: "ZGF0YQ==", ClientToken: "burst",
			})
		}()
	}

	close(start)
	wg.Wait()

	// Every caller shares one token, so each is a retry of the same create:
	// none may see a spurious ConflictException from its sibling.
	for i := range n {
		requireNoError(t, errs[i])
	}
}
