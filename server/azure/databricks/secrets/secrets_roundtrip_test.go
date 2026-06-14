package secrets_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/secrets"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(secrets.New())

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

func TestSDKSecretsRoundtrip(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	const (
		scopeName = "my-scope"
		secretKey = "db-password"
		secretVal = "hunter2"
		principal = "data-scientists"
	)

	if err := w.Secrets.CreateScope(ctx, workspace.CreateScope{Scope: scopeName}); err != nil {
		t.Fatalf("CreateScope: %v", err)
	}

	scopes, err := w.Secrets.ListScopesAll(ctx)
	if err != nil {
		t.Fatalf("ListScopes: %v", err)
	}

	if len(scopes) != 1 || scopes[0].Name != scopeName {
		t.Fatalf("unexpected scopes: %+v", scopes)
	}

	if scopes[0].BackendType != workspace.ScopeBackendTypeDatabricks {
		t.Fatalf("got backend %q, want DATABRICKS", scopes[0].BackendType)
	}

	if err = w.Secrets.PutSecret(ctx, workspace.PutSecret{
		Scope:       scopeName,
		Key:         secretKey,
		StringValue: secretVal,
	}); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}

	got, err := w.Secrets.GetSecret(ctx, workspace.GetSecretRequest{Scope: scopeName, Key: secretKey})
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	// The SDK returns the raw base64 value; "hunter2" → "aHVudGVyMg==".
	if got.Key != secretKey || got.Value != "aHVudGVyMg==" {
		t.Fatalf("unexpected secret: %+v", got)
	}

	metas, err := w.Secrets.ListSecretsAll(ctx, workspace.ListSecretsRequest{Scope: scopeName})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}

	if len(metas) != 1 || metas[0].Key != secretKey || metas[0].LastUpdatedTimestamp == 0 {
		t.Fatalf("unexpected secret metadata: %+v", metas)
	}

	if err = w.Secrets.PutAcl(ctx, workspace.PutAcl{
		Scope:      scopeName,
		Principal:  principal,
		Permission: workspace.AclPermissionManage,
	}); err != nil {
		t.Fatalf("PutAcl: %v", err)
	}

	acl, err := w.Secrets.GetAcl(ctx, workspace.GetAclRequest{Scope: scopeName, Principal: principal})
	if err != nil {
		t.Fatalf("GetAcl: %v", err)
	}

	if acl.Principal != principal || acl.Permission != workspace.AclPermissionManage {
		t.Fatalf("unexpected acl: %+v", acl)
	}

	acls, err := w.Secrets.ListAclsAll(ctx, workspace.ListAclsRequest{Scope: scopeName})
	if err != nil {
		t.Fatalf("ListAcls: %v", err)
	}

	if len(acls) != 1 || acls[0].Principal != principal {
		t.Fatalf("unexpected acls: %+v", acls)
	}

	if err = w.Secrets.DeleteSecret(ctx, workspace.DeleteSecret{Scope: scopeName, Key: secretKey}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if err = w.Secrets.DeleteAcl(ctx, workspace.DeleteAcl{Scope: scopeName, Principal: principal}); err != nil {
		t.Fatalf("DeleteAcl: %v", err)
	}

	if err = w.Secrets.DeleteScope(ctx, workspace.DeleteScope{Scope: scopeName}); err != nil {
		t.Fatalf("DeleteScope: %v", err)
	}

	remaining, err := w.Secrets.ListScopesAll(ctx)
	if err != nil {
		t.Fatalf("ListScopes after delete: %v", err)
	}

	if len(remaining) != 0 {
		t.Fatalf("expected no scopes after delete, got %+v", remaining)
	}
}

func TestPutSecretRequiresScope(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	err := w.Secrets.PutSecret(ctx, workspace.PutSecret{Scope: "missing", Key: "k", StringValue: "v"})
	if err == nil {
		t.Fatal("expected error putting secret into missing scope")
	}
}

func TestPutAclRequiresScope(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	err := w.Secrets.PutAcl(ctx, workspace.PutAcl{
		Scope:      "missing",
		Principal:  "p",
		Permission: workspace.AclPermissionRead,
	})
	if err == nil {
		t.Fatal("expected error putting acl into missing scope")
	}
}

func TestDuplicateScopeConflicts(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	if err := w.Secrets.CreateScope(ctx, workspace.CreateScope{Scope: "dup"}); err != nil {
		t.Fatalf("CreateScope: %v", err)
	}

	if err := w.Secrets.CreateScope(ctx, workspace.CreateScope{Scope: "dup"}); err == nil {
		t.Fatal("expected conflict creating duplicate scope")
	}
}
