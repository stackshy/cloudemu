package blobstorage_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/lease"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// TestSDKAcquireLeaseDoesNotCorruptContent is the regression test for the
// data-loss blocker: AcquireLease used to fall through the comp= switch into
// the plain Put Blob path (no comp=lease case existed) and zero the blob.
func TestSDKAcquireLeaseDoesNotCorruptContent(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	body := []byte("do not lose me")
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", body, nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	leaseClient, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	acq, err := leaseClient.AcquireLease(ctx, -1, nil)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	if acq.LeaseID == nil || *acq.LeaseID == "" {
		t.Fatal("AcquireLease returned no x-ms-lease-id")
	}

	if got := e.download(t, "k1"); got != string(body) {
		t.Fatalf("blob content after AcquireLease = %q, want %q (data corrupted by lease)", got, body)
	}

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ContentLength == nil || *props.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length after AcquireLease = %v, want %d", props.ContentLength, len(body))
	}
}

// TestSDKLeaseGatesWrites drives the full write-gating table for a leased
// blob: no lease id, the wrong lease id, and the correct lease id.
func TestSDKLeaseGatesWrites(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	leaseClient, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	if _, err := leaseClient.AcquireLease(ctx, 15, nil); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	bb := e.blockBlob(t, "/c1/k1")

	// No lease id: 412 LeaseIdMissing.
	_, err = bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("no-id"))), nil)
	if !bloberror.HasCode(err, bloberror.LeaseIDMissing) {
		t.Errorf("Upload with no lease id: err = %v, want LeaseIdMissing", err)
	}

	// Wrong lease id: 412 LeaseIdMismatchWithBlobOperation.
	wrongCond := &blockblob.UploadOptions{
		AccessConditions: &blob.AccessConditions{LeaseAccessConditions: &blob.LeaseAccessConditions{LeaseID: to.Ptr("not-the-lease")}},
	}
	_, err = bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("wrong-id"))), wrongCond)
	if !bloberror.HasCode(err, bloberror.LeaseIDMismatchWithBlobOperation) {
		t.Errorf("Upload with wrong lease id: err = %v, want LeaseIdMismatchWithBlobOperation", err)
	}

	if got := e.download(t, "k1"); got != "v1" {
		t.Fatalf("blob overwritten by a rejected write: %q", got)
	}

	// Correct lease id: succeeds.
	rightCond := &blockblob.UploadOptions{
		AccessConditions: &blob.AccessConditions{LeaseAccessConditions: &blob.LeaseAccessConditions{LeaseID: leaseClient.LeaseID()}},
	}
	if _, err := bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("v2"))), rightCond); err != nil {
		t.Fatalf("Upload with correct lease id: %v", err)
	}

	if got := e.download(t, "k1"); got != "v2" {
		t.Errorf("blob content = %q, want v2", got)
	}
}

// TestSDKLeaseAcquireAlreadyPresentConflicts checks that acquiring a lease
// against an already-leased blob with a different (or no) proposed id fails
// with LeaseAlreadyPresent, per the Lease Blob acquire-outcome table.
func TestSDKLeaseAcquireAlreadyPresentConflicts(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	first, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	if _, err := first.AcquireLease(ctx, -1, nil); err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}

	second, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	_, err = second.AcquireLease(ctx, -1, nil)
	if !bloberror.HasCode(err, bloberror.LeaseAlreadyPresent) {
		t.Errorf("second AcquireLease: err = %v, want LeaseAlreadyPresent", err)
	}
}

// TestSDKLeaseRenewChangeRelease exercises the renew/change/release lease
// management calls, and confirms a released blob accepts an unleased write
// again.
func TestSDKLeaseRenewChangeRelease(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	lc, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	if _, err := lc.AcquireLease(ctx, 15, nil); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	if _, err := lc.RenewLease(ctx, nil); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}

	if _, err := lc.ChangeLease(ctx, "22222222-2222-2222-2222-222222222222", nil); err != nil {
		t.Fatalf("ChangeLease: %v", err)
	}

	if got := lc.LeaseID(); got == nil || *got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("LeaseID after ChangeLease = %v, want the new id", got)
	}

	if _, err := lc.ReleaseLease(ctx, nil); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}

	// The blob is unleased again: a plain write with no lease id succeeds.
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v2"), nil); err != nil {
		t.Fatalf("UploadBuffer after release: %v", err)
	}

	if got := e.download(t, "k1"); got != "v2" {
		t.Errorf("blob content after release = %q, want v2", got)
	}
}

// TestSDKLeaseBreak checks Break Lease returns an x-ms-lease-time and that a
// write with the (still known) lease id succeeds while breaking.
func TestSDKLeaseBreak(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")

	lc, err := lease.NewBlobClient(bc, nil)
	if err != nil {
		t.Fatalf("lease.NewBlobClient: %v", err)
	}

	if _, err := lc.AcquireLease(ctx, -1, nil); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}

	brk, err := lc.BreakLease(ctx, &lease.BlobBreakOptions{BreakPeriod: to.Ptr(int32(30))})
	if err != nil {
		t.Fatalf("BreakLease: %v", err)
	}

	if brk.LeaseTime == nil {
		t.Fatal("BreakLease returned no x-ms-lease-time")
	}

	rightCond := &blockblob.UploadOptions{
		AccessConditions: &blob.AccessConditions{LeaseAccessConditions: &blob.LeaseAccessConditions{LeaseID: lc.LeaseID()}},
	}
	bb := e.blockBlob(t, "/c1/k1")

	if _, err := bb.Upload(ctx, streaming.NopCloser(bytes.NewReader([]byte("v2"))), rightCond); err != nil {
		t.Fatalf("Upload while breaking with correct lease id: %v", err)
	}
}

// TestSDKGetBlockList checks Get Block List returns uncommitted blocks after
// Stage Block and, once committed, the committed block list — the previous
// misroute 404'd because comp=blocklist GET fell through to a plain blob
// download before any commit had happened.
func TestSDKGetBlockList(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	bb := e.blockBlob(t, "/c1/k1")

	blockID := base64.StdEncoding.EncodeToString([]byte("block-a"))
	if _, err := bb.StageBlock(ctx, blockID, streaming.NopCloser(bytes.NewReader([]byte("hello"))), nil); err != nil {
		t.Fatalf("StageBlock: %v", err)
	}

	uncommitted, err := bb.GetBlockList(ctx, blockblob.BlockListTypeAll, nil)
	if err != nil {
		t.Fatalf("GetBlockList (before commit): %v", err)
	}

	if len(uncommitted.UncommittedBlocks) != 1 {
		t.Fatalf("uncommitted blocks = %d, want 1", len(uncommitted.UncommittedBlocks))
	}

	if len(uncommitted.CommittedBlocks) != 0 {
		t.Fatalf("committed blocks before commit = %d, want 0", len(uncommitted.CommittedBlocks))
	}

	if _, err := bb.CommitBlockList(ctx, []string{blockID}, nil); err != nil {
		t.Fatalf("CommitBlockList: %v", err)
	}

	committed, err := bb.GetBlockList(ctx, blockblob.BlockListTypeCommitted, nil)
	if err != nil {
		t.Fatalf("GetBlockList (after commit): %v", err)
	}

	if len(committed.CommittedBlocks) != 1 {
		t.Fatalf("committed blocks = %d, want 1", len(committed.CommittedBlocks))
	}

	if committed.CommittedBlocks[0].Size == nil || *committed.CommittedBlocks[0].Size != int64(len("hello")) {
		t.Errorf("committed block size = %v, want %d", committed.CommittedBlocks[0].Size, len("hello"))
	}
}

// TestSDKListBlobsIncludesMetadataAndAccessTier checks that List Blobs with
// Include{Metadata:true} returns the blob's metadata, and that AccessTier is
// always reported once set (List Blobs doesn't gate it behind Include).
func TestSDKListBlobsIncludesMetadataAndAccessTier(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	uploadOpts := &azblob.UploadBufferOptions{Metadata: map[string]*string{"team": to.Ptr("platform")}}
	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), uploadOpts); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")
	if _, err := bc.SetTier(ctx, blob.AccessTierCool, nil); err != nil {
		t.Fatalf("SetTier: %v", err)
	}

	cc, err := container.NewClientWithNoCredential(e.base+"/c1", &container.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("container.NewClient: %v", err)
	}

	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Include: container.ListBlobsInclude{Metadata: true},
	})

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("NextPage: %v", err)
	}

	if len(page.Segment.BlobItems) != 1 {
		t.Fatalf("blobs listed = %d, want 1", len(page.Segment.BlobItems))
	}

	item := page.Segment.BlobItems[0]

	// Unlike Set Metadata's HTTP-header round trip (canonicalized to "Team" by
	// Go's http.Header), List Blobs' metadata comes from an XML element name,
	// which isn't canonicalized — it keeps the lowercase name the driver
	// stores it under.
	if item.Metadata["team"] == nil || *item.Metadata["team"] != "platform" {
		t.Errorf("listed metadata = %v, want team=platform", item.Metadata)
	}

	if item.Properties.AccessTier == nil || *item.Properties.AccessTier != "Cool" {
		t.Errorf("listed access tier = %v, want Cool", item.Properties.AccessTier)
	}
}

// TestSDKCopyBlobPreservesBlobTypeAndTier checks that a server-side copy
// carries the source's BlobType and AccessTier to the destination, rather
// than dropping back to the BlockBlob/no-tier defaults.
func TestSDKCopyBlobPreservesBlobTypeAndTier(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "src", []byte("copy me"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	srcClient := e.blob(t, "/c1/src")
	if _, err := srcClient.SetTier(ctx, blob.AccessTierCool, nil); err != nil {
		t.Fatalf("SetTier: %v", err)
	}

	dstClient := e.blob(t, "/c1/dst")
	if _, err := dstClient.StartCopyFromURL(ctx, e.base+"/c1/src", nil); err != nil {
		t.Fatalf("StartCopyFromURL: %v", err)
	}

	props, err := dstClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.BlobType == nil || *props.BlobType != blob.BlobTypeBlockBlob {
		t.Errorf("copied blob type = %v, want BlockBlob", props.BlobType)
	}

	if props.AccessTier == nil || *props.AccessTier != "Cool" {
		t.Errorf("copied access tier = %v, want Cool", props.AccessTier)
	}
}

// TestSDKDeleteBlobWithSnapshotsConflict checks Delete Blob 409s with
// SnapshotsPresent when the blob has snapshots and no x-ms-delete-snapshots
// header is set, and succeeds once "include" is specified.
func TestSDKDeleteBlobWithSnapshotsConflict(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")
	if _, err := bc.CreateSnapshot(ctx, nil); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	_, err := e.svc.DeleteBlob(ctx, "c1", "k1", nil)
	if !bloberror.HasCode(err, bloberror.SnapshotsPresent) {
		t.Errorf("DeleteBlob with snapshots and no directive: err = %v, want SnapshotsPresent", err)
	}

	_, err = e.svc.DeleteBlob(ctx, "c1", "k1", &blob.DeleteOptions{
		DeleteSnapshots: to.Ptr(blob.DeleteSnapshotsOptionTypeInclude),
	})
	if err != nil {
		t.Fatalf("DeleteBlob with delete-snapshots=include: %v", err)
	}
}

// TestSDKArchiveTierBlocksDownload checks that a blob tiered to Archive
// rejects Download until it's moved back to an online tier, while Get
// Properties keeps working and reports the tier.
func TestSDKArchiveTierBlocksDownload(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("cold storage"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")
	if _, err := bc.SetTier(ctx, blob.AccessTierArchive, nil); err != nil {
		t.Fatalf("SetTier(Archive): %v", err)
	}

	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties on archived blob: %v", err)
	}

	if props.AccessTier == nil || *props.AccessTier != "Archive" {
		t.Errorf("access tier = %v, want Archive", props.AccessTier)
	}

	_, err = e.svc.DownloadStream(ctx, "c1", "k1", nil)
	if !bloberror.HasCode(err, bloberror.BlobArchived) {
		t.Errorf("DownloadStream on archived blob: err = %v, want BlobArchived", err)
	}

	// Rehydrating out of Archive makes it readable again.
	if _, err := bc.SetTier(ctx, blob.AccessTierHot, nil); err != nil {
		t.Fatalf("SetTier(Hot): %v", err)
	}

	if got := e.download(t, "k1"); got != "cold storage" {
		t.Errorf("content after rehydrate = %q, want %q", got, "cold storage")
	}
}

// TestSDKSetTierRejectsInvalidValue checks Set Blob Tier rejects a tier
// outside Hot/Cool/Cold/Archive.
func TestSDKSetTierRejectsInvalidValue(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	if _, err := e.svc.UploadBuffer(ctx, "c1", "k1", []byte("v1"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	bc := e.blob(t, "/c1/k1")
	if _, err := bc.SetTier(ctx, blob.AccessTier("NotATier"), nil); err == nil {
		t.Fatal("SetTier with an invalid tier value succeeded, want an error")
	}
}

// TestSDKContainerListPagination checks GET /?comp=list honors maxresults and
// marker instead of always returning every container in one page.
func TestSDKContainerListPagination(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	// c1 already exists (created by newBlobEnv); add two more.
	for _, name := range []string{"c2", "c3"} {
		if _, err := e.svc.CreateContainer(ctx, name, nil); err != nil {
			t.Fatalf("CreateContainer %s: %v", name, err)
		}
	}

	pager := e.svc.NewListContainersPager(&service.ListContainersOptions{MaxResults: to.Ptr(int32(2))})

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("NextPage: %v", err)
	}

	if len(page.ContainerItems) != 2 {
		t.Fatalf("first page = %d containers, want 2 (maxresults not honored)", len(page.ContainerItems))
	}

	if page.NextMarker == nil || *page.NextMarker == "" {
		t.Fatal("first page returned no NextMarker despite more containers remaining")
	}

	if !pager.More() {
		t.Fatal("pager.More() = false, want true (a second page remains)")
	}

	page2, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("NextPage (2nd): %v", err)
	}

	if len(page2.ContainerItems) != 1 {
		t.Fatalf("second page = %d containers, want 1", len(page2.ContainerItems))
	}
}

// TestSDKContainerACLRoundTrip checks Set/Get Container ACL: previously PUT
// ?comp=acl fell through to CreateContainer and 409'd on the already-existing
// container.
func TestSDKContainerACLRoundTrip(t *testing.T) {
	e := newBlobEnv(t)
	ctx := context.Background()

	cc, err := container.NewClientWithNoCredential(e.base+"/c1", &container.ClientOptions{ClientOptions: e.clientOpts()})
	if err != nil {
		t.Fatalf("container.NewClient: %v", err)
	}

	if _, err := cc.SetAccessPolicy(ctx, &container.SetAccessPolicyOptions{
		Access: to.Ptr(container.PublicAccessTypeContainer),
	}); err != nil {
		t.Fatalf("SetAccessPolicy: %v", err)
	}

	got, err := cc.GetAccessPolicy(ctx, nil)
	if err != nil {
		t.Fatalf("GetAccessPolicy: %v", err)
	}

	if got.BlobPublicAccess == nil || *got.BlobPublicAccess != container.PublicAccessTypeContainer {
		t.Errorf("public access = %v, want %q", got.BlobPublicAccess, container.PublicAccessTypeContainer)
	}
}
