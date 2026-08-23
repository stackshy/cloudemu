package blobstore_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/blobstore"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureBlobStoreE2E runs the real-user flow against Azure Blob Storage
// backed by a real filesystem engine (no Docker, no cloud account): create a
// container, upload a block blob, download it, stat it, copy it, download the
// copy, delete the original, and confirm a 404 — all with the real azblob SDK.
// Finally it reads the object straight off disk under the engine root, proving
// the bytes flowed through the engine rather than living only in memory.
func TestAzureBlobStoreE2E(t *testing.T) {
	eng := blobstore.New("")
	t.Cleanup(func() { _ = eng.Close() })

	cloudP := cloudemu.NewAzure(config.WithStorageEngine(eng))
	ts := httptest.NewServer(azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage}))
	t.Cleanup(ts.Close)

	opts := &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := azblob.NewClientWithNoCredential(ts.URL+"/", opts)
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	ctx := context.Background()

	const (
		container   = "c1"
		key         = "k1"
		copyKey     = "k1-copy"
		contentType = "text/plain"
	)

	body := []byte("hello from the real filesystem engine")

	// 1. Create the container — like `az storage container create`.
	if _, err := client.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// 2. Upload a block blob.
	if _, err := client.UploadBuffer(ctx, container, key, body, &azblob.UploadBufferOptions{
		Metadata:    map[string]*string{"author": ptrStr("cloudemu")},
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: ptrStr(contentType)},
	}); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// 3. Download it and verify the bytes round-trip through the engine.
	if got := download(ctx, t, client, container, key); !bytes.Equal(got, body) {
		t.Errorf("download mismatch: got=%q want=%q", got, body)
	}

	// 4. Stat it — size and content type come from the in-memory metadata.
	bbClient := client.ServiceClient().NewContainerClient(container).NewBlockBlobClient(key)

	props, err := bbClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ContentLength == nil || *props.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength=%v want %d", props.ContentLength, len(body))
	}

	if props.ContentType == nil || *props.ContentType != contentType {
		t.Errorf("ContentType=%v want %q", props.ContentType, contentType)
	}

	// 5. Copy the blob server-side and download the copy.
	dst := client.ServiceClient().NewContainerClient(container).NewBlobClient(copyKey)
	if _, err := dst.StartCopyFromURL(ctx, ts.URL+"/"+container+"/"+key, nil); err != nil {
		t.Fatalf("StartCopyFromURL: %v", err)
	}

	if got := download(ctx, t, client, container, copyKey); !bytes.Equal(got, body) {
		t.Errorf("copy download mismatch: got=%q want=%q", got, body)
	}

	// 6. Delete the original and confirm it 404s.
	if _, err := client.DeleteBlob(ctx, container, key, nil); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}

	if _, err := client.DownloadStream(ctx, container, key, nil); err == nil ||
		!strings.Contains(err.Error(), "BlobNotFound") {
		t.Errorf("expected BlobNotFound after delete, got %v", err)
	}

	// 7. Prove persistence: the copied blob's bytes are a real file on disk
	// under the engine root, and the deleted original's file is gone.
	copyPath := filepath.Join(eng.Root(), "buckets", container, "current", copyKey)

	onDisk, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("read engine file %s: %v", copyPath, err)
	}

	if !bytes.Equal(onDisk, body) {
		t.Errorf("on-disk bytes mismatch: got=%q want=%q", onDisk, body)
	}

	origPath := filepath.Join(eng.Root(), "buckets", container, "current", key)
	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Errorf("expected deleted blob's engine file to be gone, stat err=%v", err)
	}
}

func download(ctx context.Context, t *testing.T, client *azblob.Client, c, k string) []byte {
	t.Helper()

	resp, err := client.DownloadStream(ctx, c, k, nil)
	if err != nil {
		t.Fatalf("DownloadStream(%s/%s): %v", c, k, err)
	}

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s/%s: %v", c, k, err)
	}

	_ = resp.Body.Close()

	return got
}

func ptrStr(s string) *string { return &s }
