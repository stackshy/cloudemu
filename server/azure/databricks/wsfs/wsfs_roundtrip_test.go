package wsfs_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/wsfs"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(wsfs.New())

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

func TestSDKWorkspaceRoundtrip(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	const (
		dir      = "/Users/alice"
		nbPath   = "/Users/alice/notebook"
		nbSource = "print('hello cloudemu')\n"
	)

	if err := w.Workspace.Mkdirs(ctx, workspace.Mkdirs{Path: dir}); err != nil {
		t.Fatalf("Mkdirs: %v", err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(nbSource))
	if err := w.Workspace.Import(ctx, workspace.Import{
		Path:      nbPath,
		Content:   encoded,
		Format:    workspace.ImportFormatSource,
		Language:  workspace.LanguagePython,
		Overwrite: true,
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	exported, err := w.Workspace.Export(ctx, workspace.ExportRequest{
		Path:   nbPath,
		Format: workspace.ExportFormatSource,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(exported.Content)
	if err != nil {
		t.Fatalf("decode exported content: %v", err)
	}

	if string(decoded) != nbSource {
		t.Fatalf("content round-trip mismatch: got %q want %q", string(decoded), nbSource)
	}

	status, err := w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: nbPath})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	if status.ObjectType != workspace.ObjectTypeNotebook {
		t.Fatalf("got object type %q, want NOTEBOOK", status.ObjectType)
	}

	if status.Language != workspace.LanguagePython {
		t.Fatalf("got language %q, want PYTHON", status.Language)
	}

	if status.ObjectId == 0 {
		t.Fatal("expected non-zero object id")
	}

	objects, err := w.Workspace.ListAll(ctx, workspace.ListWorkspaceRequest{Path: dir})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(objects) != 1 {
		t.Fatalf("got %d objects, want 1", len(objects))
	}

	if objects[0].Path != nbPath || objects[0].ObjectType != workspace.ObjectTypeNotebook {
		t.Fatalf("unexpected list entry: %+v", objects[0])
	}

	dirStatus, err := w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: dir})
	if err != nil {
		t.Fatalf("GetStatus dir: %v", err)
	}

	if dirStatus.ObjectType != workspace.ObjectTypeDirectory {
		t.Fatalf("got dir object type %q, want DIRECTORY", dirStatus.ObjectType)
	}

	if err = w.Workspace.Delete(ctx, workspace.Delete{Path: dir, Recursive: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err = w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: nbPath}); err == nil {
		t.Fatal("expected error getting status of deleted notebook")
	}

	if _, err = w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: dir}); err == nil {
		t.Fatal("expected error getting status of deleted directory")
	}
}
