package gitcredentials_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/gitcredentials"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(gitcredentials.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "x",
		Credentials: config.PatCredentials{},
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

func TestSDKGitCredentialLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.GitCredentials.Create(ctx, workspace.CreateCredentialsRequest{
		GitProvider:         "gitHub",
		GitUsername:         "octocat",
		PersonalAccessToken: "ghp_secret",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.CredentialId == 0 {
		t.Fatal("expected non-zero credential id")
	}

	if created.GitProvider != "gitHub" || created.GitUsername != "octocat" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	got, err := w.GitCredentials.GetByCredentialId(ctx, created.CredentialId)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.CredentialId != created.CredentialId || got.GitUsername != "octocat" {
		t.Fatalf("unexpected get response: %+v", got)
	}

	all, err := w.GitCredentials.ListAll(ctx, workspace.ListCredentialsRequest{})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 1 || all[0].CredentialId != created.CredentialId {
		t.Fatalf("unexpected list: %+v", all)
	}

	if err = w.GitCredentials.Update(ctx, workspace.UpdateCredentialsRequest{
		CredentialId:        created.CredentialId,
		GitProvider:         "gitLab",
		GitUsername:         "newuser",
		PersonalAccessToken: "glpat_secret",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := w.GitCredentials.GetByCredentialId(ctx, created.CredentialId)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if updated.GitProvider != "gitLab" || updated.GitUsername != "newuser" {
		t.Fatalf("update not applied: %+v", updated)
	}

	if err = w.GitCredentials.DeleteByCredentialId(ctx, created.CredentialId); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err = w.GitCredentials.GetByCredentialId(ctx, created.CredentialId); err == nil {
		t.Fatal("expected error getting deleted credential")
	}
}

func TestSDKGitCredentialGetMissing(t *testing.T) {
	w := newWorkspace(t)

	if _, err := w.GitCredentials.GetByCredentialId(context.Background(), 999); err == nil {
		t.Fatal("expected error for missing credential")
	}
}
