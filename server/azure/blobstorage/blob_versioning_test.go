package blobstorage_test

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/stackshy/cloudemu/v2"
	blobprovider "github.com/stackshy/cloudemu/v2/providers/azure/blobstorage"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestSDKBlobVersioning drives the real azblob client through the full blob
// versioning lifecycle: two uploads produce two versions, each addressable by
// its version id; a versions listing marks the current version; deleting a
// specific version removes only that one; deleting the base blob leaves the
// versions listable.
func TestSDKBlobVersioning(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewAzure()

	// Enable account-level versioning (the ARM Set Blob Service Properties plane
	// in a real deployment) for the single account the data plane models.
	if err := cloudP.BlobStorage.SetBlobServiceProperties(ctx, blobprovider.AccountName,
		storagedriver.BlobServiceProperties{IsVersioningEnabled: true}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

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

	// First upload -> version 1.
	up1, err := svcClient.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil)
	if err != nil {
		t.Fatalf("upload v1: %v", err)
	}

	if up1.VersionID == nil || *up1.VersionID == "" {
		t.Fatalf("first upload returned no x-ms-version-id")
	}

	firstVersion := *up1.VersionID

	// Second upload (overwrite) -> version 2, now current.
	up2, err := svcClient.UploadBuffer(ctx, "c1", "k1", []byte("v2-overwritten"), nil)
	if err != nil {
		t.Fatalf("upload v2: %v", err)
	}

	if up2.VersionID == nil || *up2.VersionID == firstVersion {
		t.Fatalf("second upload version id = %v, want a new distinct id", up2.VersionID)
	}

	currentVersion := *up2.VersionID

	// The base blob serves the current (second) content.
	assertBlobBody(t, ctx, svcClient, "c1", "k1", "v2-overwritten")

	ctr := svcClient.ServiceClient().NewContainerClient("c1")

	// Read the first version by id.
	blobV1, err := ctr.NewBlobClient("k1").WithVersionID(firstVersion)
	if err != nil {
		t.Fatalf("WithVersionID(first): %v", err)
	}

	dlV1, err := blobV1.DownloadStream(ctx, nil)
	if err != nil {
		t.Fatalf("download first version: %v", err)
	}

	if got := readAll(t, dlV1.Body); got != "v1" {
		t.Fatalf("first version body = %q, want %q", got, "v1")
	}

	// List with include=versions -> both versions, current one marked.
	versions := listVersions(t, ctx, ctr)
	if len(versions) != 2 {
		t.Fatalf("listed %d versions, want 2", len(versions))
	}

	current, seenFirst := "", false
	for _, v := range versions {
		if v.id == firstVersion {
			seenFirst = true
			if v.isCurrent {
				t.Fatalf("first version should not be current")
			}
		}
		if v.isCurrent {
			current = v.id
		}
	}

	if !seenFirst {
		t.Fatalf("first version %q missing from versions listing", firstVersion)
	}

	if current != currentVersion {
		t.Fatalf("current version in listing = %q, want %q", current, currentVersion)
	}

	// Delete the first (non-current) version -> gone, current remains.
	delV1, err := ctr.NewBlobClient("k1").WithVersionID(firstVersion)
	if err != nil {
		t.Fatalf("WithVersionID(first) for delete: %v", err)
	}

	if _, err := delV1.Delete(ctx, nil); err != nil {
		t.Fatalf("delete first version: %v", err)
	}

	if got := listVersions(t, ctx, ctr); len(got) != 1 {
		t.Fatalf("after version delete, listed %d versions, want 1", len(got))
	}

	// The current version is still readable via the base blob.
	assertBlobBody(t, ctx, svcClient, "c1", "k1", "v2-overwritten")

	// Delete the base blob -> with versioning on, the current version is retained
	// and stays listable.
	if _, err := svcClient.DeleteBlob(ctx, "c1", "k1", nil); err != nil {
		t.Fatalf("delete base blob: %v", err)
	}

	if got := listVersions(t, ctx, ctr); len(got) != 1 {
		t.Fatalf("after base delete, listed %d versions, want 1 (versions retained)", len(got))
	}
}

type versionItem struct {
	id        string
	isCurrent bool
}

func listVersions(t *testing.T, ctx context.Context, ctr *container.Client) []versionItem {
	t.Helper()

	pager := ctr.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Include: container.ListBlobsInclude{Versions: true},
	})

	var out []versionItem

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list versions: %v", err)
		}

		for _, b := range page.Segment.BlobItems {
			if b.VersionID == nil {
				continue
			}

			out = append(out, versionItem{
				id:        *b.VersionID,
				isCurrent: b.IsCurrentVersion != nil && *b.IsCurrentVersion,
			})
		}
	}

	return out
}

func assertBlobBody(t *testing.T, ctx context.Context, c *azblob.Client, container, blob, want string) {
	t.Helper()

	dl, err := c.DownloadStream(ctx, container, blob, nil)
	if err != nil {
		t.Fatalf("download %s/%s: %v", container, blob, err)
	}

	if got := readAll(t, dl.Body); got != want {
		t.Fatalf("blob %s/%s body = %q, want %q", container, blob, got, want)
	}
}

func readAll(t *testing.T, r io.ReadCloser) string {
	t.Helper()

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	_ = r.Close()

	return string(b)
}
