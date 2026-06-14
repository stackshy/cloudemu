package queryhistory_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/sql"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/queryhistory"
)

func newClient(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(queryhistory.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "test-token",
		Credentials: config.PatCredentials{},
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

// TestSDKQueryHistoryList pins issue #223: w.QueryHistory.List previously
// returned "no handler registered". It must now succeed (with an empty result,
// since the in-memory backend runs no queries).
func TestSDKQueryHistoryList(t *testing.T) {
	w := newClient(t)

	res, err := w.QueryHistory.List(context.Background(), sql.ListQueryHistoryRequest{})
	if err != nil {
		t.Fatalf("QueryHistory.List: %v", err)
	}

	if len(res.Res) != 0 {
		t.Fatalf("got %d query-history entries, want 0", len(res.Res))
	}
}
