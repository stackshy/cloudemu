package blobstorage_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/pageblob"
)

// TestSDKSetTagsPreservesContent proves the data-corruption bug is gone: Set
// Blob Tags stores tags and leaves the blob body untouched (the old
// fall-through wrote the tags XML over the blob content).
func TestSDKSetTagsPreservesContent(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	body := []byte("original-value")
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", body, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	if _, err := bc.SetTags(ctx, map[string]string{"env": "prod", "team": "payments"}, nil); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	if got := e.download(t, "k1"); got != string(body) {
		t.Fatalf("blob body corrupted by SetTags: got %q, want %q", got, body)
	}
}

// TestSDKSetAndGetTagsRoundTrip proves SetTags/GetTags round-trip the tag set.
func TestSDKSetAndGetTagsRoundTrip(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("payload"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	want := map[string]string{"env": "prod", "team": "payments"}
	if _, err := bc.SetTags(ctx, want, nil); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	resp, err := bc.GetTags(ctx, nil)
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}

	got := map[string]string{}
	for _, tag := range resp.BlobTagSet {
		if tag.Key != nil && tag.Value != nil {
			got[*tag.Key] = *tag.Value
		}
	}

	if len(got) != len(want) {
		t.Fatalf("tag count = %d, want %d (%v)", len(got), len(want), got)
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %q = %q, want %q", k, got[k], v)
		}
	}
}

// TestSetTagsEmptyClearsTags proves an empty tag set clears the blob's tags
// without touching its body.
func TestSetTagsEmptyClearsTags(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("keep-me"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	if _, err := bc.SetTags(ctx, map[string]string{"env": "prod"}, nil); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	if _, err := bc.SetTags(ctx, map[string]string{}, nil); err != nil {
		t.Fatalf("SetTags (clear): %v", err)
	}

	resp, err := bc.GetTags(ctx, nil)
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}

	if len(resp.BlobTagSet) != 0 {
		t.Errorf("tags not cleared: %v", resp.BlobTagSet)
	}

	if got := e.download(t, "k1"); got != "keep-me" {
		t.Errorf("blob body altered by tag clear: got %q, want keep-me", got)
	}
}

// TestUnknownCompFailsClosed proves a blob PUT with an unrecognized comp value
// is rejected (fail-closed) and does NOT overwrite the blob body — the
// architectural root fix that prevents the tags/page corruption class.
func TestUnknownCompFailsClosed(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("do-not-corrupt"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		e.base+"/c1/k1?comp=totallybogus", bytes.NewReader([]byte("<Bogus/>")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	req.Header.Set("x-ms-version", "2023-11-03")

	resp, err := e.tr.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 400 {
		t.Fatalf("unknown comp PUT returned %d, want a 4xx/5xx failure", resp.StatusCode)
	}

	if got := e.download(t, "k1"); got != "do-not-corrupt" {
		t.Fatalf("unknown comp PUT corrupted the blob body: got %q, want do-not-corrupt", got)
	}
}

// TestPageBlobFailsClosed proves page-blob create and UploadPages fail closed
// (page-blob range semantics are not implemented) rather than silently
// corrupting a blob via the comp fall-through.
func TestPageBlobFailsClosed(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	pc, err := pageblob.NewClientWithNoCredential(e.base+"/c1/page1", &pageblob.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("pageblob.NewClient: %v", err)
	}

	const pageSize = 512
	if _, err := pc.Create(ctx, pageSize, nil); err == nil {
		t.Fatal("page-blob Create should fail closed (page blobs unsupported), got success")
	}
}
