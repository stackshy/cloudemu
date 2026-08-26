package blobstorage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/appendblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
)

// condWrongETag is an ETag that never matches a real blob, used to force an
// If-Match mismatch.
const condWrongETag = azcore.ETag(`"0xDEADBEEFDEADBEEF"`)

// condIfMatch builds the AccessConditions carrying an If-Match precondition.
func condIfMatch(e azcore.ETag) *blob.AccessConditions {
	return &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: &e}}
}

// requireHTTPStatus asserts err is an azcore.ResponseError with the given HTTP
// status code.
func requireHTTPStatus(t *testing.T, err error, want int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected HTTP %d error, got nil", want)
	}

	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected *azcore.ResponseError with status %d, got %T: %v", want, err, err)
	}

	if re.StatusCode != want {
		t.Fatalf("status = %d, want %d", re.StatusCode, want)
	}
}

// rawStatus issues a bare HTTP request through the test transport and returns
// the response status code. It is used for conditions the typed SDK never puts
// on the wire (Set Blob Tier forwards only x-ms-if-tags) or surfaces as a
// non-error (Download treats 304 as a success code), so the wire behavior must
// be asserted directly.
func (e *blobEnv) rawStatus(t *testing.T, method, path string, headers map[string]string) int {
	t.Helper()

	req, err := http.NewRequest(method, e.base+path, nil)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e.tr.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}

	_ = resp.Body.Close()

	return resp.StatusCode
}

// currentETag returns a blob's current ETag via GetProperties.
func (e *blobEnv) currentETag(t *testing.T, path string) azcore.ETag {
	t.Helper()

	props, err := e.blob(t, path).GetProperties(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetProperties(%s): %v", path, err)
	}

	if props.ETag == nil {
		t.Fatalf("GetProperties(%s): nil ETag", path)
	}

	return *props.ETag
}

func TestSDKConditionalCommitBlockList(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	bb := e.blockBlob(t, "/c1/cbl")

	stage := func(id, data string) string {
		bid := base64.StdEncoding.EncodeToString([]byte(id))
		if _, err := bb.StageBlock(ctx, bid, streaming.NopCloser(bytes.NewReader([]byte(data))), nil); err != nil {
			t.Fatalf("StageBlock %q: %v", id, err)
		}

		return bid
	}

	a := stage("blk-a", "v1")
	if _, err := bb.CommitBlockList(ctx, []string{a}, nil); err != nil {
		t.Fatalf("initial CommitBlockList: %v", err)
	}

	etag := e.currentETag(t, "/c1/cbl")

	b := stage("blk-b", "v2")

	// Mismatched If-Match must fail with 412 and leave the blob unchanged.
	_, err := bb.CommitBlockList(ctx, []string{b}, &blockblob.CommitBlockListOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	if got := e.download(t, "cbl"); got != "v1" {
		t.Errorf("blob mutated despite failed If-Match: %q", got)
	}

	// Matching If-Match succeeds.
	if _, err := bb.CommitBlockList(ctx, []string{b}, &blockblob.CommitBlockListOptions{AccessConditions: condIfMatch(etag)}); err != nil {
		t.Fatalf("CommitBlockList with matching If-Match: %v", err)
	}

	if got := e.download(t, "cbl"); got != "v2" {
		t.Errorf("content after matching commit = %q, want v2", got)
	}
}

func TestSDKConditionalSetMetadata(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "sm", []byte("body"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	etag := e.currentETag(t, "/c1/sm")
	bc := e.blob(t, "/c1/sm")
	meta := map[string]*string{"k": ptr("v")}

	_, err := bc.SetMetadata(ctx, meta, &blob.SetMetadataOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	if _, err := bc.SetMetadata(ctx, meta, &blob.SetMetadataOptions{AccessConditions: condIfMatch(etag)}); err != nil {
		t.Fatalf("SetMetadata with matching If-Match: %v", err)
	}
}

func TestSDKConditionalSetProperties(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "sp", []byte("body"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	etag := e.currentETag(t, "/c1/sp")
	bc := e.blob(t, "/c1/sp")
	headers := blob.HTTPHeaders{BlobContentType: ptr("text/plain")}

	_, err := bc.SetHTTPHeaders(ctx, headers, &blob.SetHTTPHeadersOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	if _, err := bc.SetHTTPHeaders(ctx, headers, &blob.SetHTTPHeadersOptions{AccessConditions: condIfMatch(etag)}); err != nil {
		t.Fatalf("SetHTTPHeaders with matching If-Match: %v", err)
	}
}

func TestSDKConditionalSetTier(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "st", []byte("body"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	etag := e.currentETag(t, "/c1/st")

	// The azblob SetTier client forwards only x-ms-if-tags, so If-Match is
	// asserted at the wire level directly.
	if got := e.rawStatus(t, http.MethodPut, "/c1/st?comp=tier",
		map[string]string{"x-ms-access-tier": "Cool", "If-Match": string(condWrongETag)}); got != http.StatusPreconditionFailed {
		t.Errorf("SetTier mismatched If-Match status = %d, want 412", got)
	}

	if got := e.rawStatus(t, http.MethodPut, "/c1/st?comp=tier",
		map[string]string{"x-ms-access-tier": "Cool", "If-Match": string(etag)}); got != http.StatusOK {
		t.Errorf("SetTier matching If-Match status = %d, want 200", got)
	}
}

func TestSDKConditionalAppendBlock(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	ac, err := appendblob.NewClientWithNoCredential(e.base+"/c1/ab", &appendblob.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("appendblob.NewClient: %v", err)
	}

	if _, err := ac.Create(ctx, nil); err != nil {
		t.Fatalf("Create append blob: %v", err)
	}

	etag := e.currentETag(t, "/c1/ab")
	body := streaming.NopCloser(bytes.NewReader([]byte("chunk")))

	_, err = ac.AppendBlock(ctx, body, &appendblob.AppendBlockOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	if _, err := ac.AppendBlock(ctx, streaming.NopCloser(bytes.NewReader([]byte("chunk"))),
		&appendblob.AppendBlockOptions{AccessConditions: condIfMatch(etag)}); err != nil {
		t.Fatalf("AppendBlock with matching If-Match: %v", err)
	}
}

func TestSDKConditionalDelete(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "del", []byte("body"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	etag := e.currentETag(t, "/c1/del")
	bc := e.blob(t, "/c1/del")

	_, err := bc.Delete(ctx, &blob.DeleteOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	// Still present after the failed conditional delete.
	if _, err := bc.GetProperties(ctx, nil); err != nil {
		t.Fatalf("blob deleted despite failed If-Match: %v", err)
	}

	if _, err := bc.Delete(ctx, &blob.DeleteOptions{AccessConditions: condIfMatch(etag)}); err != nil {
		t.Fatalf("Delete with matching If-Match: %v", err)
	}

	if _, err := bc.GetProperties(ctx, nil); err == nil {
		t.Fatal("blob still present after matching conditional delete")
	}
}

func TestSDKGetConditionalReads(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "rd", []byte("payload"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	etag := e.currentETag(t, "/c1/rd")

	// GET with If-None-Match matching the current ETag -> 304 Not Modified.
	// The azblob client treats 304 as a success status, so assert the wire
	// status directly.
	if got := e.rawStatus(t, http.MethodGet, "/c1/rd",
		map[string]string{"If-None-Match": string(etag)}); got != http.StatusNotModified {
		t.Errorf("GET If-None-Match:<current> status = %d, want 304", got)
	}

	// HEAD with If-None-Match matching -> 304 Not Modified.
	if got := e.rawStatus(t, http.MethodHead, "/c1/rd",
		map[string]string{"If-None-Match": string(etag)}); got != http.StatusNotModified {
		t.Errorf("HEAD If-None-Match:<current> status = %d, want 304", got)
	}

	// GET with a mismatched If-Match -> 412 Precondition Failed (the SDK
	// surfaces this as an error).
	_, err := e.svc.DownloadStream(ctx, "c1", "rd", &blob.DownloadStreamOptions{AccessConditions: condIfMatch(condWrongETag)})
	requireHTTPStatus(t, err, http.StatusPreconditionFailed)

	// A matching If-Match read still succeeds and returns the body.
	dl, err := e.svc.DownloadStream(ctx, "c1", "rd", &blob.DownloadStreamOptions{AccessConditions: condIfMatch(etag)})
	if err != nil {
		t.Fatalf("DownloadStream with matching If-Match: %v", err)
	}

	_ = dl.Body.Close()
}

func TestSDKCopyMetadataOverride(t *testing.T) {
	env := newSuiteEnv(t)
	ctx := context.Background()

	for _, c := range []string{"src-c", "dst-c"} {
		if _, err := env.client.CreateContainer(ctx, c, nil); err != nil {
			t.Fatalf("CreateContainer(%s): %v", c, err)
		}
	}

	_, err := env.client.UploadBuffer(ctx, "src-c", "orig", []byte("data"), &azblob.UploadBufferOptions{
		Metadata: map[string]*string{"orig": ptr("yes")},
	})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// Copy supplying fresh metadata -> destination has EXACTLY that, nothing
	// inherited from the source.
	dst := env.client.ServiceClient().NewContainerClient("dst-c").NewBlobClient("over")

	_, err = dst.StartCopyFromURL(ctx, env.ts.URL+"/src-c/orig", &blob.StartCopyFromURLOptions{
		Metadata: map[string]*string{"fresh": ptr("new")},
	})
	if err != nil {
		t.Fatalf("StartCopyFromURL(override): %v", err)
	}

	props, err := dst.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties(over): %v", err)
	}

	if v, ok := metaGet(props.Metadata, "fresh"); !ok || v != "new" {
		t.Errorf("dst metadata fresh=%q ok=%v, want new", v, ok)
	}

	if _, ok := metaGet(props.Metadata, "orig"); ok {
		t.Error("dst inherited source metadata despite override (full replace expected)")
	}
}

func TestSDKCopyInheritsMetadata(t *testing.T) {
	env := newSuiteEnv(t)
	ctx := context.Background()

	for _, c := range []string{"src-c", "dst-c"} {
		if _, err := env.client.CreateContainer(ctx, c, nil); err != nil {
			t.Fatalf("CreateContainer(%s): %v", c, err)
		}
	}

	_, err := env.client.UploadBuffer(ctx, "src-c", "orig", []byte("data"), &azblob.UploadBufferOptions{
		Metadata: map[string]*string{"orig": ptr("yes")},
	})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	// Copy with no metadata -> destination inherits the source's metadata.
	dst := env.client.ServiceClient().NewContainerClient("dst-c").NewBlobClient("inh")

	if _, err := dst.StartCopyFromURL(ctx, env.ts.URL+"/src-c/orig", nil); err != nil {
		t.Fatalf("StartCopyFromURL(inherit): %v", err)
	}

	props, err := dst.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties(inh): %v", err)
	}

	if v, ok := metaGet(props.Metadata, "orig"); !ok || v != "yes" {
		t.Errorf("dst metadata orig=%q ok=%v, want yes (inherited)", v, ok)
	}
}
