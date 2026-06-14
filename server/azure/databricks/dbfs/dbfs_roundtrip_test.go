package dbfs_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/files"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/dbfs"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(dbfs.New())

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

func TestSDKDbfsPutReadRoundtrip(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	want := []byte("hello dbfs roundtrip")
	p := "/tmp/cloudemu/hello.txt"

	if err := w.Dbfs.Put(ctx, files.Put{
		Path:      p,
		Contents:  base64.StdEncoding.EncodeToString(want),
		Overwrite: true,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res, err := w.Dbfs.Read(ctx, files.ReadDbfsRequest{Path: p})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		t.Fatalf("decode read data: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("round-tripped bytes = %q, want %q", got, want)
	}

	if res.BytesRead != int64(len(want)) {
		t.Fatalf("bytes_read = %d, want %d", res.BytesRead, len(want))
	}
}

func TestSDKDbfsGetStatusAndList(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	want := []byte("status-and-list")
	p := "/data/sub/file.bin"

	if err := w.Dbfs.Put(ctx, files.Put{
		Path:      p,
		Contents:  base64.StdEncoding.EncodeToString(want),
		Overwrite: true,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	st, err := w.Dbfs.GetStatusByPath(ctx, p)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}

	if st.IsDir {
		t.Fatal("GetStatus: expected file, got dir")
	}

	if st.FileSize != int64(len(want)) {
		t.Fatalf("GetStatus file_size = %d, want %d", st.FileSize, len(want))
	}

	entries, err := w.Dbfs.ListAll(ctx, files.ListDbfsRequest{Path: "/data/sub"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 1 || entries[0].Path != p {
		t.Fatalf("List = %+v, want single entry %q", entries, p)
	}
}

func TestSDKDbfsMkdirsListAndDelete(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	if err := w.Dbfs.Mkdirs(ctx, files.MkDirs{Path: "/proj/logs"}); err != nil {
		t.Fatalf("Mkdirs: %v", err)
	}

	st, err := w.Dbfs.GetStatusByPath(ctx, "/proj/logs")
	if err != nil {
		t.Fatalf("GetStatus dir: %v", err)
	}

	if !st.IsDir {
		t.Fatal("expected /proj/logs to be a directory")
	}

	entries, err := w.Dbfs.ListAll(ctx, files.ListDbfsRequest{Path: "/proj"})
	if err != nil {
		t.Fatalf("List /proj: %v", err)
	}

	if len(entries) != 1 || !entries[0].IsDir || entries[0].Path != "/proj/logs" {
		t.Fatalf("List /proj = %+v, want single dir /proj/logs", entries)
	}

	if err := w.Dbfs.Delete(ctx, files.Delete{Path: "/proj", Recursive: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := w.Dbfs.GetStatusByPath(ctx, "/proj"); err == nil {
		t.Fatal("GetStatus after delete: expected error, got nil")
	}
}

func TestSDKDbfsWriteFileBlockUpload(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	want := bytes.Repeat([]byte("block-upload-payload;"), 100)
	p := "/streamed/data.bin"

	// WriteFile drives the create / add-block / close stream API.
	if err := w.Dbfs.WriteFile(ctx, p, want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := w.Dbfs.ReadFile(ctx, p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("round-tripped %d bytes, want %d bytes (mismatch)", len(got), len(want))
	}
}

func TestSDKDbfsReadMissingFile(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	if _, err := w.Dbfs.Read(ctx, files.ReadDbfsRequest{Path: "/does/not/exist"}); err == nil {
		t.Fatal("Read missing file: expected error, got nil")
	}
}
