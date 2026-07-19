// Package azure_test contains E2E campaign tests for the STORAGE cell
// (azure / sdk-compat): real-user journeys driving the official
// azure-sdk-for-go azblob client against the CloudEmu Azure server mounted in
// an httptest.Server.
//
// The Azure Blob HTTP surface (server/azure/blob/handler.go) covers: list
// containers, container create/delete, list blobs (prefix/delimiter/
// maxresults/marker), blob put/get/head/delete, and copy via
// x-ms-copy-source. Versioning, lifecycle, tags, leases, snapshots, and block
// lists are driver-only and not exposed over HTTP, so they are not exercised
// here.
package azure_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// campaignEnv bundles the emulator-backed test server and a real azblob
// client pointed at it.
type campaignEnv struct {
	ts     *httptest.Server
	client *azblob.Client
}

// newCampaignEnv builds a fresh emulator, mounts only the blob-storage
// driver, and returns a real SDK client. Anonymous access avoids forging
// SharedKey signatures; MaxRetries: -1 disables retries so error assertions
// see the first response.
func newCampaignEnv(t *testing.T) *campaignEnv {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})

	ts := httptest.NewServer(srv)
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

	return &campaignEnv{ts: ts, client: client}
}

func ptr[T any](v T) *T { return &v }

// download fetches a blob and returns its full body.
func (e *campaignEnv) download(ctx context.Context, t *testing.T, c, k string) []byte {
	t.Helper()

	resp, err := e.client.DownloadStream(ctx, c, k, nil)
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

// metaGet looks up a metadata key case-insensitively — the emulator
// lowercases x-ms-meta-* names and the SDK re-canonicalizes header keys, so
// casing round-trips are not stable.
func metaGet(meta map[string]*string, key string) (string, bool) {
	for k, v := range meta {
		if strings.EqualFold(k, key) && v != nil {
			return *v, true
		}
	}

	return "", false
}

// TestE2ECampaignFullLifecycle walks a complete user journey: create
// container, upload blobs with varied content types (including empty and
// ~1MB payloads), download, stat, list, copy, delete blobs, delete
// container, and confirm the blob is gone.
func TestE2ECampaignFullLifecycle(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()
	cName := "lifecycle"

	if _, err := env.client.CreateContainer(ctx, cName, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// ~1MB deterministic binary payload.
	bigBody := make([]byte, 1<<20)
	for i := range bigBody {
		bigBody[i] = byte(i * 31 % 251)
	}

	blobs := []struct {
		key         string
		body        []byte
		contentType string
	}{
		{"doc.txt", []byte("plain text body"), "text/plain"},
		{"data.json", []byte(`{"ok":true}`), "application/json"},
		{"empty.bin", []byte{}, "application/octet-stream"},
		{"big.bin", bigBody, "application/octet-stream"},
	}

	for _, b := range blobs {
		_, err := env.client.UploadBuffer(ctx, cName, b.key, b.body, &azblob.UploadBufferOptions{
			HTTPHeaders: &blob.HTTPHeaders{BlobContentType: ptr(b.contentType)},
			Metadata:    map[string]*string{"origin": ptr("campaign")},
		})
		if err != nil {
			t.Fatalf("UploadBuffer(%s): %v", b.key, err)
		}
	}

	// Download each blob and verify bytes round-trip exactly.
	for _, b := range blobs {
		got := env.download(ctx, t, cName, b.key)
		if !bytes.Equal(got, b.body) {
			t.Errorf("%s: body mismatch got %d bytes want %d bytes", b.key, len(got), len(b.body))
		}
	}

	// Stat (HEAD) each blob: length, content type, metadata.
	for _, b := range blobs {
		bc := env.client.ServiceClient().NewContainerClient(cName).NewBlobClient(b.key)

		props, err := bc.GetProperties(ctx, nil)
		if err != nil {
			t.Fatalf("GetProperties(%s): %v", b.key, err)
		}

		if props.ContentLength == nil || *props.ContentLength != int64(len(b.body)) {
			t.Errorf("%s: ContentLength=%v want %d", b.key, props.ContentLength, len(b.body))
		}

		gotCT := ""
		if props.ContentType != nil {
			gotCT = *props.ContentType
		}

		if gotCT != b.contentType {
			t.Errorf("%s: ContentType=%q want %q", b.key, gotCT, b.contentType)
		}

		if v, ok := metaGet(props.Metadata, "origin"); !ok || v != "campaign" {
			t.Errorf("%s: metadata origin=%q ok=%v want campaign", b.key, v, ok)
		}
	}

	// Flat list: all four keys, lexically sorted.
	var listed []string

	pager := env.client.NewListBlobsFlatPager(cName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListBlobsFlat: %v", err)
		}

		for _, item := range page.Segment.BlobItems {
			listed = append(listed, *item.Name)
		}
	}

	wantOrder := []string{"big.bin", "data.json", "doc.txt", "empty.bin"}
	if fmt.Sprint(listed) != fmt.Sprint(wantOrder) {
		t.Errorf("flat list=%v want %v (lexically sorted)", listed, wantOrder)
	}

	// Copy doc.txt -> copy/doc-copy.txt within the container; the emulator
	// preserves the source ETag (sha256 of body) on the copy.
	srcClient := env.client.ServiceClient().NewContainerClient(cName).NewBlobClient("doc.txt")

	srcProps, err := srcClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties(src): %v", err)
	}

	dstClient := env.client.ServiceClient().NewContainerClient(cName).NewBlobClient("copy/doc-copy.txt")

	copyResp, err := dstClient.StartCopyFromURL(ctx, env.ts.URL+"/"+cName+"/doc.txt", nil)
	if err != nil {
		t.Fatalf("StartCopyFromURL: %v", err)
	}

	if copyResp.CopyStatus == nil || *copyResp.CopyStatus != blob.CopyStatusTypeSuccess {
		t.Errorf("CopyStatus=%v want success", copyResp.CopyStatus)
	}

	if got := env.download(ctx, t, cName, "copy/doc-copy.txt"); !bytes.Equal(got, []byte("plain text body")) {
		t.Errorf("copied body=%q want %q", got, "plain text body")
	}

	dstProps, err := dstClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties(dst): %v", err)
	}

	if srcProps.ETag == nil || dstProps.ETag == nil || *srcProps.ETag != *dstProps.ETag {
		t.Errorf("copy ETag=%v want source ETag=%v (emulator preserves ETag)", dstProps.ETag, srcProps.ETag)
	}

	// Delete all blobs, then the container.
	for _, k := range append(wantOrder, "copy/doc-copy.txt") {
		if _, err := env.client.DeleteBlob(ctx, cName, k, nil); err != nil {
			t.Errorf("DeleteBlob(%s): %v", k, err)
		}
	}

	if _, err := env.client.DeleteContainer(ctx, cName, nil); err != nil {
		t.Fatalf("DeleteContainer: %v", err)
	}

	if _, err := env.client.DownloadStream(ctx, cName, "doc.txt", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("download after container delete: err=%v want BlobNotFound", err)
	}
}

// TestE2ECampaignOverwriteReplacesBlob verifies a second upload to the same
// key fully replaces content and produces a new ETag (sha256 of body).
func TestE2ECampaignOverwriteReplacesBlob(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	if _, err := env.client.CreateContainer(ctx, "ow", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := env.client.UploadBuffer(ctx, "ow", "k", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer v1: %v", err)
	}

	bc := env.client.ServiceClient().NewContainerClient("ow").NewBlobClient("k")

	p1, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties v1: %v", err)
	}

	if _, err := env.client.UploadBuffer(ctx, "ow", "k", []byte("v2 different"), nil); err != nil {
		t.Fatalf("UploadBuffer v2: %v", err)
	}

	p2, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties v2: %v", err)
	}

	if got := env.download(ctx, t, "ow", "k"); string(got) != "v2 different" {
		t.Errorf("body after overwrite=%q want %q", got, "v2 different")
	}

	if p1.ETag == nil || p2.ETag == nil || *p1.ETag == *p2.ETag {
		t.Errorf("ETag unchanged after overwrite: v1=%v v2=%v", p1.ETag, p2.ETag)
	}
}

// TestE2ECampaignListPrefixDelimiterPagination covers hierarchical listing
// (delimiter roll-up into BlobPrefixes), prefix filtering, and marker-based
// pagination with maxresults.
func TestE2ECampaignListPrefixDelimiterPagination(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()
	cName := "listing"

	if _, err := env.client.CreateContainer(ctx, cName, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	keys := []string{
		"logs/2024/a.txt",
		"logs/2024/b.txt",
		"logs/2025/c.txt",
		"root-1.txt",
		"root-2.txt",
	}
	for _, k := range keys {
		if _, err := env.client.UploadBuffer(ctx, cName, k, []byte("x"), nil); err != nil {
			t.Fatalf("UploadBuffer(%s): %v", k, err)
		}
	}

	cClient := env.client.ServiceClient().NewContainerClient(cName)

	// Hierarchy listing at root: "logs/" rolls up, root-*.txt listed flat.
	{
		var gotBlobs, gotPrefixes []string

		hp := cClient.NewListBlobsHierarchyPager("/", nil)
		for hp.More() {
			page, err := hp.NextPage(ctx)
			if err != nil {
				t.Fatalf("hierarchy list: %v", err)
			}

			for _, b := range page.Segment.BlobItems {
				gotBlobs = append(gotBlobs, *b.Name)
			}

			for _, p := range page.Segment.BlobPrefixes {
				gotPrefixes = append(gotPrefixes, *p.Name)
			}
		}

		if fmt.Sprint(gotBlobs) != fmt.Sprint([]string{"root-1.txt", "root-2.txt"}) {
			t.Errorf("root blobs=%v want [root-1.txt root-2.txt]", gotBlobs)
		}

		if fmt.Sprint(gotPrefixes) != fmt.Sprint([]string{"logs/"}) {
			t.Errorf("root prefixes=%v want [logs/]", gotPrefixes)
		}
	}

	// Prefix + delimiter one level down: two sub-prefixes, no blobs.
	{
		var gotBlobs, gotPrefixes []string

		hp := cClient.NewListBlobsHierarchyPager("/", &container.ListBlobsHierarchyOptions{
			Prefix: ptr("logs/"),
		})
		for hp.More() {
			page, err := hp.NextPage(ctx)
			if err != nil {
				t.Fatalf("hierarchy list logs/: %v", err)
			}

			for _, b := range page.Segment.BlobItems {
				gotBlobs = append(gotBlobs, *b.Name)
			}

			for _, p := range page.Segment.BlobPrefixes {
				gotPrefixes = append(gotPrefixes, *p.Name)
			}
		}

		if len(gotBlobs) != 0 {
			t.Errorf("logs/ blobs=%v want none (all rolled up)", gotBlobs)
		}

		if fmt.Sprint(gotPrefixes) != fmt.Sprint([]string{"logs/2024/", "logs/2025/"}) {
			t.Errorf("logs/ prefixes=%v want [logs/2024/ logs/2025/]", gotPrefixes)
		}
	}

	// Flat prefix filter.
	{
		var got []string

		fp := env.client.NewListBlobsFlatPager(cName, &azblob.ListBlobsFlatOptions{
			Prefix: ptr("logs/2024/"),
		})
		for fp.More() {
			page, err := fp.NextPage(ctx)
			if err != nil {
				t.Fatalf("flat list prefix: %v", err)
			}

			for _, b := range page.Segment.BlobItems {
				got = append(got, *b.Name)
			}
		}

		if fmt.Sprint(got) != fmt.Sprint([]string{"logs/2024/a.txt", "logs/2024/b.txt"}) {
			t.Errorf("prefix list=%v want [logs/2024/a.txt logs/2024/b.txt]", got)
		}
	}

	// Pagination: 5 blobs, maxresults=2 -> 3 pages via NextMarker
	// continuation tokens, keys in lexical order across pages.
	{
		var got []string

		pages := 0

		fp := env.client.NewListBlobsFlatPager(cName, &azblob.ListBlobsFlatOptions{
			MaxResults: ptr(int32(2)),
		})
		for fp.More() {
			page, err := fp.NextPage(ctx)
			if err != nil {
				t.Fatalf("paginated list: %v", err)
			}

			pages++
			if len(page.Segment.BlobItems) > 2 {
				t.Errorf("page %d has %d items, exceeds maxresults=2", pages, len(page.Segment.BlobItems))
			}

			for _, b := range page.Segment.BlobItems {
				got = append(got, *b.Name)
			}
		}

		if pages != 3 {
			t.Errorf("pages=%d want 3 (2+2+1)", pages)
		}

		if fmt.Sprint(got) != fmt.Sprint(keys) {
			t.Errorf("paginated keys=%v want %v", got, keys)
		}
	}
}

// TestE2ECampaignEmptyContainerList verifies listing a freshly created
// container yields zero blobs and a terminating pager.
func TestE2ECampaignEmptyContainerList(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	if _, err := env.client.CreateContainer(ctx, "empty-c", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	pages, items := 0, 0

	pager := env.client.NewListBlobsFlatPager("empty-c", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list empty container: %v", err)
		}

		pages++
		items += len(page.Segment.BlobItems)
	}

	if pages != 1 || items != 0 {
		t.Errorf("pages=%d items=%d want 1 page, 0 items", pages, items)
	}
}

// TestE2ECampaignListContainersSorted verifies the service-level container
// list returns all containers sorted by name.
func TestE2ECampaignListContainersSorted(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	for _, name := range []string{"zeta", "alpha", "mid"} {
		if _, err := env.client.CreateContainer(ctx, name, nil); err != nil {
			t.Fatalf("CreateContainer(%s): %v", name, err)
		}
	}

	var got []string

	pager := env.client.ServiceClient().NewListContainersPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListContainers: %v", err)
		}

		for _, c := range page.ContainerItems {
			got = append(got, *c.Name)
		}
	}

	if fmt.Sprint(got) != fmt.Sprint([]string{"alpha", "mid", "zeta"}) {
		t.Errorf("containers=%v want [alpha mid zeta] (sorted)", got)
	}
}

// TestE2ECampaignErrorCases covers the typed-error surface the SDK sees:
// missing blob/container reads and deletes, duplicate container creation,
// deleting a non-empty container, and copying from a missing source.
func TestE2ECampaignErrorCases(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	if _, err := env.client.CreateContainer(ctx, "errs", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// Get nonexistent blob in an existing container.
	if _, err := env.client.DownloadStream(ctx, "errs", "missing", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("download missing blob: err=%v want BlobNotFound", err)
	}

	// Delete nonexistent blob.
	if _, err := env.client.DeleteBlob(ctx, "errs", "missing", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("delete missing blob: err=%v want BlobNotFound", err)
	}

	// Get blob from a nonexistent container. NOTE emulator quirk: the driver
	// NotFound maps uniformly to BlobNotFound, never ContainerNotFound (real
	// Azure returns ContainerNotFound here).
	if _, err := env.client.DownloadStream(ctx, "no-such-container", "k", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("download from missing container: err=%v want BlobNotFound (emulator maps all NotFound)", err)
	}

	// Delete nonexistent container -> same uniform NotFound mapping.
	if _, err := env.client.DeleteContainer(ctx, "no-such-container", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("delete missing container: err=%v want BlobNotFound (emulator maps all NotFound)", err)
	}

	// HEAD nonexistent blob: error code arrives via x-ms-error-code header.
	bc := env.client.ServiceClient().NewContainerClient("errs").NewBlobClient("missing")
	if _, err := bc.GetProperties(ctx, nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("head missing blob: err=%v want BlobNotFound", err)
	}

	// Duplicate container creation.
	if _, err := env.client.CreateContainer(ctx, "errs", nil); !bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		t.Errorf("duplicate create: err=%v want ContainerAlreadyExists", err)
	}

	// Delete non-empty container. The driver returns FailedPrecondition,
	// which the handler maps to 500 InternalError (real Azure deletes
	// containers recursively, so this whole failure mode is emulator-only).
	if _, err := env.client.UploadBuffer(ctx, "errs", "occupant", []byte("x"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	_, err := env.client.DeleteContainer(ctx, "errs", nil)
	if err == nil {
		t.Fatalf("delete non-empty container succeeded, want error")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("delete non-empty container: err=%T want *azcore.ResponseError", err)
	}

	if respErr.StatusCode != 500 || respErr.ErrorCode != "InternalError" {
		t.Errorf("delete non-empty container: status=%d code=%q want 500 InternalError", respErr.StatusCode, respErr.ErrorCode)
	}

	// Copy from a missing source blob.
	dst := env.client.ServiceClient().NewContainerClient("errs").NewBlobClient("copy-dst")
	if _, err := dst.StartCopyFromURL(ctx, env.ts.URL+"/errs/missing-src", nil); !bloberror.HasCode(err, bloberror.BlobNotFound) {
		t.Errorf("copy from missing source: err=%v want BlobNotFound", err)
	}
}

// TestE2ECampaignKeyNamesSlashesUnicode verifies blob names containing
// slashes, unicode, and spaces survive the SDK's URL escaping and round-trip
// through upload, list, download, and delete.
func TestE2ECampaignKeyNamesSlashesUnicode(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()
	cName := "names"

	if _, err := env.client.CreateContainer(ctx, cName, nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	keys := []string{
		"deep/nested/path/file.txt",
		"unicode-éüñ-日本語.txt",
		"with space and+plus.txt",
	}

	for i, k := range keys {
		body := []byte(fmt.Sprintf("payload-%d", i))
		if _, err := env.client.UploadBuffer(ctx, cName, k, body, nil); err != nil {
			t.Fatalf("UploadBuffer(%q): %v", k, err)
		}
	}

	// Listing returns the raw (unescaped) names.
	seen := map[string]bool{}

	pager := env.client.NewListBlobsFlatPager(cName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		for _, b := range page.Segment.BlobItems {
			seen[*b.Name] = true
		}
	}

	for _, k := range keys {
		if !seen[k] {
			t.Errorf("key %q missing from list %v", k, seen)
		}
	}

	// Download and delete by the raw name.
	for i, k := range keys {
		want := fmt.Sprintf("payload-%d", i)
		if got := env.download(ctx, t, cName, k); string(got) != want {
			t.Errorf("download(%q)=%q want %q", k, got, want)
		}

		if _, err := env.client.DeleteBlob(ctx, cName, k, nil); err != nil {
			t.Errorf("DeleteBlob(%q): %v", k, err)
		}
	}
}

// TestE2ECampaignMetadataRoundTrip verifies multi-key metadata survives
// upload -> HEAD and upload -> GET (headers on both paths), with the
// emulator's lowercasing of x-ms-meta-* names.
func TestE2ECampaignMetadataRoundTrip(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	if _, err := env.client.CreateContainer(ctx, "meta", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	_, err := env.client.UploadBuffer(ctx, "meta", "k", []byte("body"), &azblob.UploadBufferOptions{
		Metadata: map[string]*string{
			"team":    ptr("storage"),
			"release": ptr("2026-07"),
		},
	})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := env.client.ServiceClient().NewContainerClient("meta").NewBlobClient("k")

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	for k, want := range map[string]string{"team": "storage", "release": "2026-07"} {
		if got, ok := metaGet(props.Metadata, k); !ok || got != want {
			t.Errorf("HEAD metadata[%s]=%q ok=%v want %q", k, got, ok, want)
		}
	}

	dl, err := env.client.DownloadStream(ctx, "meta", "k", nil)
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}

	_, _ = io.Copy(io.Discard, dl.Body)
	_ = dl.Body.Close()

	for k, want := range map[string]string{"team": "storage", "release": "2026-07"} {
		if got, ok := metaGet(dl.Metadata, k); !ok || got != want {
			t.Errorf("GET metadata[%s]=%q ok=%v want %q", k, got, ok, want)
		}
	}
}

// TestE2ECampaignCrossContainerCopy verifies copying between two containers
// preserves content and metadata (driver deep-copies both) and that the
// source remains intact.
func TestE2ECampaignCrossContainerCopy(t *testing.T) {
	env := newCampaignEnv(t)
	ctx := context.Background()

	for _, c := range []string{"src-c", "dst-c"} {
		if _, err := env.client.CreateContainer(ctx, c, nil); err != nil {
			t.Fatalf("CreateContainer(%s): %v", c, err)
		}
	}

	body := []byte("cross-container payload")

	_, err := env.client.UploadBuffer(ctx, "src-c", "orig", body, &azblob.UploadBufferOptions{
		Metadata: map[string]*string{"stage": ptr("prod")},
	})
	if err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	dst := env.client.ServiceClient().NewContainerClient("dst-c").NewBlobClient("copied")

	if _, err := dst.StartCopyFromURL(ctx, env.ts.URL+"/src-c/orig", nil); err != nil {
		t.Fatalf("StartCopyFromURL: %v", err)
	}

	if got := env.download(ctx, t, "dst-c", "copied"); !bytes.Equal(got, body) {
		t.Errorf("copied body=%q want %q", got, body)
	}

	props, err := dst.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties(dst): %v", err)
	}

	if v, ok := metaGet(props.Metadata, "stage"); !ok || v != "prod" {
		t.Errorf("copied metadata stage=%q ok=%v want prod (driver copies metadata)", v, ok)
	}

	// Source untouched.
	if got := env.download(ctx, t, "src-c", "orig"); !bytes.Equal(got, body) {
		t.Errorf("source body changed after copy: %q", got)
	}
}
