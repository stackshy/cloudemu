package token_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/settings"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/token"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(token.New())

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

func TestSDKTokenLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Tokens.Create(ctx, settings.CreateTokenRequest{
		Comment:         "ci-token",
		LifetimeSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.TokenValue == "" {
		t.Fatal("expected token value")
	}

	if created.TokenInfo == nil || created.TokenInfo.TokenId == "" {
		t.Fatalf("expected token info with id, got %+v", created.TokenInfo)
	}

	if created.TokenInfo.Comment != "ci-token" {
		t.Fatalf("got comment %q, want ci-token", created.TokenInfo.Comment)
	}

	if created.TokenInfo.CreationTime == 0 {
		t.Fatal("expected non-zero creation time")
	}

	if created.TokenInfo.ExpiryTime != created.TokenInfo.CreationTime+3600*1000 {
		t.Fatalf("unexpected expiry time: %d", created.TokenInfo.ExpiryTime)
	}

	infos, err := w.Tokens.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(infos) != 1 {
		t.Fatalf("got %d tokens, want 1", len(infos))
	}

	if infos[0].TokenId != created.TokenInfo.TokenId {
		t.Fatalf("got token id %q, want %q", infos[0].TokenId, created.TokenInfo.TokenId)
	}

	if err = w.Tokens.Delete(ctx, settings.RevokeTokenRequest{TokenId: created.TokenInfo.TokenId}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	infos, err = w.Tokens.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll after delete: %v", err)
	}

	if len(infos) != 0 {
		t.Fatalf("got %d tokens after delete, want 0", len(infos))
	}
}

func TestSDKTokenNoExpiry(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Tokens.Create(ctx, settings.CreateTokenRequest{Comment: "no-expiry"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.TokenInfo.ExpiryTime != 0 {
		t.Fatalf("got expiry %d, want 0 (no expiry)", created.TokenInfo.ExpiryTime)
	}
}

func TestSDKTokenDeleteMissing(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	err := w.Tokens.Delete(ctx, settings.RevokeTokenRequest{TokenId: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error deleting missing token")
	}
}
