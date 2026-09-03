package blobstorage_test

import (
	"context"
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

// newSoftDeleteClient stands up an Azure blob wire server whose account has the
// given Blob service properties, returning a real azblob client pointed at it.
func newSoftDeleteClient(t *testing.T, props storagedriver.BlobServiceProperties) *azblob.Client {
	t.Helper()

	ctx := context.Background()
	cloudP := cloudemu.NewAzure()

	if err := cloudP.BlobStorage.SetBlobServiceProperties(ctx, blobprovider.AccountName, props); err != nil {
		t.Fatalf("set blob service properties: %v", err)
	}

	srv := azureserver.New(azureserver.Drivers{BlobStorage: cloudP.BlobStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := azblob.NewClientWithNoCredential(ts.URL+"/", &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatalf("NewClientWithNoCredential: %v", err)
	}

	return client
}

// listDeleted returns the soft-deleted blobs of a container (List Blobs
// include=deleted), keyed by name with their reported remaining retention days.
func listDeleted(t *testing.T, ctx context.Context, ctr *container.Client) map[string]int32 {
	t.Helper()

	pager := ctr.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Include: container.ListBlobsInclude{Deleted: true},
	})

	out := make(map[string]int32)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list include=deleted: %v", err)
		}

		for _, b := range page.Segment.BlobItems {
			if b.Deleted == nil || !*b.Deleted {
				continue
			}

			var remaining int32
			if b.Properties != nil && b.Properties.RemainingRetentionDays != nil {
				remaining = *b.Properties.RemainingRetentionDays
			}

			out[*b.Name] = remaining
		}
	}

	return out
}

// TestSDKBlobSoftDelete drives the real azblob client through the soft-delete
// lifecycle: with the account delete-retention policy enabled a Delete Blob
// retains the blob (a normal list omits it, include=deleted surfaces it with a
// remaining-retention countdown), and Undelete Blob restores it.
func TestSDKBlobSoftDelete(t *testing.T) {
	ctx := context.Background()

	client := newSoftDeleteClient(t, storagedriver.BlobServiceProperties{
		DeleteRetentionEnabled: true,
		DeleteRetentionDays:    7,
	})

	if _, err := client.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := client.UploadBuffer(ctx, "c1", "k1", []byte("hello"), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Delete with soft delete on -> retained, not removed.
	if _, err := client.DeleteBlob(ctx, "c1", "k1", nil); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	ctr := client.ServiceClient().NewContainerClient("c1")

	// A normal list omits the soft-deleted blob.
	if names := listBlobNames(t, ctx, ctr); len(names) != 0 {
		t.Fatalf("normal list after delete = %v, want empty", names)
	}

	// A direct download of the soft-deleted blob fails (it is not active).
	if _, err := client.DownloadStream(ctx, "c1", "k1", nil); err == nil {
		t.Fatalf("download of soft-deleted blob should fail")
	}

	// include=deleted surfaces it with a remaining-retention countdown.
	deleted := listDeleted(t, ctx, ctr)
	if remaining, ok := deleted["k1"]; !ok {
		t.Fatalf("soft-deleted blob k1 missing from include=deleted listing: %v", deleted)
	} else if remaining != 7 {
		t.Fatalf("RemainingRetentionDays = %d, want 7", remaining)
	}

	// Undelete restores the blob to active.
	if _, err := ctr.NewBlobClient("k1").Undelete(ctx, nil); err != nil {
		t.Fatalf("undelete: %v", err)
	}

	assertBlobBody(t, ctx, client, "c1", "k1", "hello")

	if names := listBlobNames(t, ctx, ctr); len(names) != 1 || names[0] != "k1" {
		t.Fatalf("list after undelete = %v, want [k1]", names)
	}

	if got := listDeleted(t, ctx, ctr); len(got) != 0 {
		t.Fatalf("include=deleted after undelete = %v, want empty", got)
	}
}

// TestSDKBlobSoftDeleteDisabledHardDeletes confirms that with no delete
// retention policy a Delete Blob is a permanent hard delete (unchanged
// behavior): the blob is gone and nothing surfaces under include=deleted.
func TestSDKBlobSoftDeleteDisabledHardDeletes(t *testing.T) {
	ctx := context.Background()

	client := newSoftDeleteClient(t, storagedriver.BlobServiceProperties{})

	if _, err := client.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := client.UploadBuffer(ctx, "c1", "k1", []byte("hello"), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := client.DeleteBlob(ctx, "c1", "k1", nil); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	ctr := client.ServiceClient().NewContainerClient("c1")

	if got := listDeleted(t, ctx, ctr); len(got) != 0 {
		t.Fatalf("include=deleted with soft delete off = %v, want empty (hard delete)", got)
	}

	// Undelete of a hard-deleted blob has nothing to restore.
	if _, err := ctr.NewBlobClient("k1").Undelete(ctx, nil); err == nil {
		t.Fatalf("undelete of hard-deleted blob should fail")
	}
}

// TestSDKBlobSoftDeleteCoexistsWithVersioning confirms that when versioning is
// also enabled, deleting a blob keeps the version-based recovery path intact
// (the version stays listable) rather than diverting to soft delete.
func TestSDKBlobSoftDeleteCoexistsWithVersioning(t *testing.T) {
	ctx := context.Background()

	client := newSoftDeleteClient(t, storagedriver.BlobServiceProperties{
		IsVersioningEnabled:    true,
		DeleteRetentionEnabled: true,
		DeleteRetentionDays:    7,
	})

	if _, err := client.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	up, err := client.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if up.VersionID == nil || *up.VersionID == "" {
		t.Fatalf("versioning enabled but upload returned no version id")
	}

	if _, err := client.DeleteBlob(ctx, "c1", "k1", nil); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	ctr := client.ServiceClient().NewContainerClient("c1")

	// With versioning on, the version is retained and listable; the base blob is
	// not soft-deleted (versions are the recovery mechanism).
	if got := listVersions(t, ctx, ctr); len(got) != 1 {
		t.Fatalf("versions after delete = %d, want 1 (retained)", len(got))
	}

	if got := listDeleted(t, ctx, ctr); len(got) != 0 {
		t.Fatalf("include=deleted with versioning on = %v, want empty", got)
	}
}

// listBlobNames returns the live blob names of a container in listing order.
func listBlobNames(t *testing.T, ctx context.Context, ctr *container.Client) []string {
	t.Helper()

	pager := ctr.NewListBlobsFlatPager(nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list blobs: %v", err)
		}

		for _, b := range page.Segment.BlobItems {
			names = append(names, *b.Name)
		}
	}

	return names
}
