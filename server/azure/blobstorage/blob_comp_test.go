package blobstorage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/appendblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// blobEnv is an httptest server over the Azure blob wire handler plus the
// transport and a no-credential service client the real azblob sub-clients
// share, so tests exercise the actual data-plane wire protocol.
type blobEnv struct {
	base string
	tr   policy.Transporter
	svc  *azblob.Client
}

func newBlobEnv(t *testing.T) *blobEnv {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	tr := ts.Client()

	svc, err := azblob.NewClientWithNoCredential(ts.URL+"/", &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: tr, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	if _, err := svc.CreateContainer(context.Background(), "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	return &blobEnv{base: ts.URL, tr: tr, svc: svc}
}

func (e *blobEnv) clientOpts() policy.ClientOptions {
	return policy.ClientOptions{Transport: e.tr, Retry: policy.RetryOptions{MaxRetries: -1}}
}

func (e *blobEnv) blockBlob(t *testing.T, path string) *blockblob.Client {
	t.Helper()

	c, err := blockblob.NewClientWithNoCredential(e.base+path, &blockblob.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("blockblob.NewClient: %v", err)
	}

	return c
}

func (e *blobEnv) blob(t *testing.T, path string) *blob.Client {
	t.Helper()

	c, err := blob.NewClientWithNoCredential(e.base+path, &blob.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("blob.NewClient: %v", err)
	}

	return c
}

// download reads a blob's content through the service client.
func (e *blobEnv) download(t *testing.T, key string) string {
	t.Helper()

	dl, err := e.svc.DownloadStream(context.Background(), "c1", key, nil)
	if err != nil {
		t.Fatalf("DownloadStream %q: %v", key, err)
	}

	data, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(data)
}

func TestSDKStageAndCommitBlockList(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	bb := e.blockBlob(t, "/c1/k1")

	blocks := [][]byte{[]byte("Hello, "), []byte("block "), []byte("world")}
	ids := make([]string, len(blocks))

	for i, b := range blocks {
		id := base64.StdEncoding.EncodeToString([]byte("block-" + string(rune('a'+i))))
		ids[i] = id

		if _, err := bb.StageBlock(ctx, id, streaming.NopCloser(bytes.NewReader(b)), nil); err != nil {
			t.Fatalf("StageBlock %d: %v", i, err)
		}
	}

	if _, err := bb.CommitBlockList(ctx, ids, nil); err != nil {
		t.Fatalf("CommitBlockList: %v", err)
	}

	if got := e.download(t, "k1"); got != "Hello, block world" {
		t.Errorf("committed blob = %q, want %q", got, "Hello, block world")
	}
}

func TestSDKSetMetadataPreservesContent(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	body := []byte("do not wipe me")
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", body, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	resp, err := bc.SetMetadata(ctx, map[string]*string{"stage": to.Ptr("qa")}, nil)
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	if resp.ETag == nil {
		t.Error("SetMetadata returned no ETag")
	}

	if got := e.download(t, "k1"); got != string(body) {
		t.Errorf("content after SetMetadata = %q, want %q", got, body)
	}

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	// HTTP canonicalizes the x-ms-meta-<name> suffix, so the round-tripped key
	// comes back title-cased ("Stage").
	if props.Metadata["Stage"] == nil || *props.Metadata["Stage"] != "qa" {
		t.Errorf("metadata not persisted: %v", props.Metadata)
	}
}

func TestSDKSetHTTPHeadersPreservesContent(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	body := []byte("keep this body intact")
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", body, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	if _, err := bc.SetHTTPHeaders(ctx, blob.HTTPHeaders{BlobContentType: to.Ptr("text/plain")}, nil); err != nil {
		t.Fatalf("SetHTTPHeaders: %v", err)
	}

	if got := e.download(t, "k1"); got != string(body) {
		t.Errorf("content after SetHTTPHeaders = %q, want %q", got, body)
	}

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ContentType == nil || *props.ContentType != "text/plain" {
		t.Errorf("content type = %v, want text/plain", props.ContentType)
	}
}

func TestSDKSetTierPreservesContent(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	body := []byte("tiered content")
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", body, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	if _, err := bc.SetTier(ctx, blob.AccessTierCool, nil); err != nil {
		t.Fatalf("SetTier: %v", err)
	}

	if got := e.download(t, "k1"); got != string(body) {
		t.Errorf("content after SetTier = %q, want %q", got, body)
	}

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.AccessTier == nil || *props.AccessTier != "Cool" {
		t.Errorf("access tier = %v, want Cool", props.AccessTier)
	}
}

func TestSDKConditionalPutIfNoneMatch(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	bb := e.blockBlob(t, "/c1/k1")

	cond := &blockblob.UploadOptions{
		AccessConditions: &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)},
		},
	}

	if _, err := bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("first"))), cond); err != nil {
		t.Fatalf("first conditional upload: %v", err)
	}

	if _, err := bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("second"))), cond); err == nil {
		t.Fatal("second conditional upload should have failed on existing blob")
	}

	if got := e.download(t, "k1"); got != "first" {
		t.Errorf("blob overwritten despite If-None-Match:* : %q", got)
	}
}

func TestSDKCreateSnapshot(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("version-1"), nil); err != nil {
		t.Fatalf("UploadBuffer v1: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	snap, err := bc.CreateSnapshot(ctx, nil)
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if snap.Snapshot == nil || *snap.Snapshot == "" {
		t.Fatal("CreateSnapshot returned empty x-ms-snapshot")
	}

	// Overwrite the base blob; the snapshot must still read the original bytes.
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("version-2"), nil); err != nil {
		t.Fatalf("UploadBuffer v2: %v", err)
	}

	if got := e.download(t, "k1"); got != "version-2" {
		t.Errorf("base blob = %q, want version-2", got)
	}

	snapClient, err := bc.WithSnapshot(*snap.Snapshot)
	if err != nil {
		t.Fatalf("WithSnapshot: %v", err)
	}

	dl, err := snapClient.DownloadStream(ctx, nil)
	if err != nil {
		t.Fatalf("snapshot DownloadStream: %v", err)
	}

	got, _ := io.ReadAll(dl.Body)
	if string(got) != "version-1" {
		t.Errorf("snapshot content = %q, want version-1", got)
	}
}

func TestSDKAppendBlob(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	ac, err := appendblob.NewClientWithNoCredential(e.base+"/c1/k1", &appendblob.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("appendblob.NewClient: %v", err)
	}

	if _, err := ac.Create(ctx, nil); err != nil {
		t.Fatalf("Create append blob: %v", err)
	}

	for _, part := range []string{"one ", "two ", "three"} {
		if _, err := ac.AppendBlock(ctx, streaming.NopCloser(bytes.NewReader([]byte(part))), nil); err != nil {
			t.Fatalf("AppendBlock %q: %v", part, err)
		}
	}

	if got := e.download(t, "k1"); got != "one two three" {
		t.Errorf("append blob content = %q, want %q", got, "one two three")
	}

	props, err := ac.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.BlobType == nil || *props.BlobType != blob.BlobTypeAppendBlob {
		t.Errorf("blob type = %v, want AppendBlob", props.BlobType)
	}
}

func TestSDKSetContainerMetadata(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	cc, err := container.NewClientWithNoCredential(e.base+"/c1", &container.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("container.NewClient: %v", err)
	}

	if _, err := cc.SetMetadata(ctx, &container.SetMetadataOptions{
		Metadata: map[string]*string{"team": to.Ptr("platform")},
	}); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	props, err := cc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.Metadata["Team"] == nil || *props.Metadata["Team"] != "platform" {
		t.Errorf("container metadata = %v, want team=platform", props.Metadata)
	}
}
