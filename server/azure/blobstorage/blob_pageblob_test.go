package blobstorage_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/pageblob"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKPageBlobGetPageRanges drives the real azblob page-blob client through a
// create → write two non-adjacent pages → Get Page Ranges → clear one → re-read
// lifecycle, asserting the reported ranges match exactly the written pages.
func TestSDKPageBlobGetPageRanges(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewAzure()

	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	clientOpts := &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	svcClient, err := azblob.NewClientWithNoCredential(ts.URL+"/", clientOpts)
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	pbOpts := &pageblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	pbClient, err := pageblob.NewClientWithNoCredential(ts.URL+"/c1/pb1", pbOpts)
	if err != nil {
		t.Fatalf("pageblob NewClientWithNoCredential: %v", err)
	}

	// A 2048-byte page blob is four 512-byte pages, all zero, no ranges yet.
	const blobSize = 2048
	if _, err := pbClient.Create(ctx, blobSize, nil); err != nil {
		t.Fatalf("Create page blob: %v", err)
	}

	if got := getPageRanges(t, ctx, pbClient); len(got) != 0 {
		t.Fatalf("fresh page blob has %d ranges, want 0", len(got))
	}

	// Write page 0 (bytes 0-511) and page 2 (bytes 1024-1535): two non-adjacent
	// runs, so Get Page Ranges must report exactly two ranges.
	page0 := bytes.Repeat([]byte{0xAB}, 512)
	page2 := bytes.Repeat([]byte{0xCD}, 512)

	uploadPage(t, ctx, pbClient, 0, page0)
	uploadPage(t, ctx, pbClient, 1024, page2)

	ranges := getPageRanges(t, ctx, pbClient)
	wantTwo := []pageblob.PageRange{
		{Start: ptr64(0), End: ptr64(511)},
		{Start: ptr64(1024), End: ptr64(1535)},
	}
	assertRanges(t, ranges, wantTwo)

	// The blob content reflects both written pages over a zero background.
	assertPageBlobBody(t, ctx, svcClient, page0, page2)

	// Clearing page 0 leaves only the page-2 range.
	if _, err := pbClient.ClearPages(ctx, blob.HTTPRange{Offset: 0, Count: 512}, nil); err != nil {
		t.Fatalf("ClearPages: %v", err)
	}

	assertRanges(t, getPageRanges(t, ctx, pbClient), []pageblob.PageRange{
		{Start: ptr64(1024), End: ptr64(1535)},
	})

	// Page 0 now reads back as zeros; page 2 is untouched.
	assertPageBlobBody(t, ctx, svcClient, make([]byte, 512), page2)
}

// TestSDKPageBlobAdjacentPagesCoalesce verifies that two adjacent written pages
// are reported as a single coalesced range, matching real Azure.
func TestSDKPageBlobAdjacentPagesCoalesce(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	pbOpts := &pageblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}

	svcClient, err := azblob.NewClientWithNoCredential(ts.URL+"/",
		&azblob.ClientOptions{ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}}})
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	pbClient, err := pageblob.NewClientWithNoCredential(ts.URL+"/c1/pb1", pbOpts)
	if err != nil {
		t.Fatalf("pageblob NewClientWithNoCredential: %v", err)
	}

	if _, err := pbClient.Create(ctx, 2048, nil); err != nil {
		t.Fatalf("Create page blob: %v", err)
	}

	uploadPage(t, ctx, pbClient, 0, bytes.Repeat([]byte{1}, 512))
	uploadPage(t, ctx, pbClient, 512, bytes.Repeat([]byte{2}, 512))

	assertRanges(t, getPageRanges(t, ctx, pbClient), []pageblob.PageRange{
		{Start: ptr64(0), End: ptr64(1023)},
	})
}

func uploadPage(t *testing.T, ctx context.Context, c *pageblob.Client, offset int64, data []byte) {
	t.Helper()

	body := streaming.NopCloser(bytes.NewReader(data))
	if _, err := c.UploadPages(ctx, body, blob.HTTPRange{Offset: offset, Count: int64(len(data))}, nil); err != nil {
		t.Fatalf("UploadPages at %d: %v", offset, err)
	}
}

func getPageRanges(t *testing.T, ctx context.Context, c *pageblob.Client) []*pageblob.PageRange {
	t.Helper()

	pager := c.NewGetPageRangesPager(nil)

	var out []*pageblob.PageRange

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("GetPageRanges: %v", err)
		}

		out = append(out, page.PageRange...)
	}

	return out
}

func assertRanges(t *testing.T, got []*pageblob.PageRange, want []pageblob.PageRange) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d page ranges, want %d: %+v", len(got), len(want), dumpRanges(got))
	}

	for i, w := range want {
		if got[i].Start == nil || got[i].End == nil || *got[i].Start != *w.Start || *got[i].End != *w.End {
			t.Fatalf("range %d = %s, want {Start:%d End:%d}", i, dumpRanges(got[i:i+1]), *w.Start, *w.End)
		}
	}
}

func dumpRanges(rs []*pageblob.PageRange) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		s, e := int64(-1), int64(-1)
		if r.Start != nil {
			s = *r.Start
		}

		if r.End != nil {
			e = *r.End
		}

		out = append(out, fmt.Sprintf("[%d-%d]", s, e))
	}

	return strings.Join(out, " ")
}

func assertPageBlobBody(t *testing.T, ctx context.Context, c *azblob.Client, page0, page2 []byte) {
	t.Helper()

	dl, err := c.DownloadStream(ctx, "c1", "pb1", nil)
	if err != nil {
		t.Fatalf("download page blob: %v", err)
	}

	body := readAll(t, dl.Body)

	want := make([]byte, 2048)
	copy(want[0:512], page0)
	copy(want[1024:1536], page2)

	if body != string(want) {
		t.Fatalf("page blob body mismatch: len(got)=%d len(want)=%d", len(body), len(want))
	}
}

func ptr64(v int64) *int64 { return &v }
