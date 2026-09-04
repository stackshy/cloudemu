package blobstorage_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// immutabilityFixture spins up the Azure blob data plane behind a FakeClock and
// returns the service client plus the clock, so tests can drive the WORM
// retention window deterministically.
func immutabilityFixture(t *testing.T) (*azblob.Client, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(fc))

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

	return svcClient, fc
}

func requireImmutableError(t *testing.T, err error, wantCode string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected %s error, got nil", wantCode)
	}

	if !bloberror.HasCode(err, bloberror.Code(wantCode)) {
		t.Fatalf("error = %v, want code %s", err, wantCode)
	}
}

// TestSDKBlobImmutabilityPolicy drives the real azblob client through a
// time-based retention immutability policy: an unlocked policy blocks delete and
// overwrite within its window, both are permitted again once the retain-until
// date elapses (FakeClock advanced), and Get Blob Properties echoes the policy.
func TestSDKBlobImmutabilityPolicy(t *testing.T) {
	ctx := context.Background()

	svcClient, fc := immutabilityFixture(t)

	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := svcClient.UploadBuffer(ctx, "c1", "doc", []byte("original"), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	blobClient := svcClient.ServiceClient().NewContainerClient("c1").NewBlobClient("doc")

	until := fc.Now().Add(24 * time.Hour)

	setResp, err := blobClient.SetImmutabilityPolicy(ctx, until, &blob.SetImmutabilityPolicyOptions{
		Mode: to.Ptr(blob.ImmutabilityPolicySettingUnlocked),
	})
	if err != nil {
		t.Fatalf("SetImmutabilityPolicy: %v", err)
	}

	if setResp.ImmutabilityPolicyMode == nil || !strings.EqualFold(string(*setResp.ImmutabilityPolicyMode), "unlocked") {
		t.Fatalf("set policy mode = %v, want unlocked", setResp.ImmutabilityPolicyMode)
	}

	// Get Blob Properties echoes the policy headers.
	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ImmutabilityPolicyMode == nil || !strings.EqualFold(string(*props.ImmutabilityPolicyMode), "unlocked") {
		t.Fatalf("props policy mode = %v, want unlocked", props.ImmutabilityPolicyMode)
	}

	if props.ImmutabilityPolicyExpiresOn == nil || !props.ImmutabilityPolicyExpiresOn.Equal(until.UTC().Truncate(time.Second)) {
		t.Fatalf("props expiry = %v, want %v", props.ImmutabilityPolicyExpiresOn, until.UTC().Truncate(time.Second))
	}

	// Within the window: delete and overwrite are both blocked.
	if _, err := svcClient.DeleteBlob(ctx, "c1", "doc", nil); !isBlocked(err) {
		t.Fatalf("delete within window = %v, want blocked", err)
	}

	requireImmutableError(t,
		mustErr(svcClient.UploadBuffer(ctx, "c1", "doc", []byte("overwrite"), nil)),
		"BlobImmutableDueToPolicy")

	// The original content is untouched by the blocked overwrite.
	assertBlobBody(t, ctx, svcClient, "c1", "doc", "original")

	// Advance past the retain-until date: WORM protection lifts.
	fc.Advance(25 * time.Hour)

	if _, err := svcClient.UploadBuffer(ctx, "c1", "doc", []byte("now-editable"), nil); err != nil {
		t.Fatalf("overwrite after expiry: %v", err)
	}

	if _, err := svcClient.DeleteBlob(ctx, "c1", "doc", nil); err != nil {
		t.Fatalf("delete after expiry: %v", err)
	}
}

// TestSDKBlobImmutabilityLocked verifies a locked policy can only be extended,
// never shortened or deleted, while still blocking delete within its window.
func TestSDKBlobImmutabilityLocked(t *testing.T) {
	ctx := context.Background()

	svcClient, fc := immutabilityFixture(t)

	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := svcClient.UploadBuffer(ctx, "c1", "ledger", []byte("record"), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	blobClient := svcClient.ServiceClient().NewContainerClient("c1").NewBlobClient("ledger")

	locked := fc.Now().Add(48 * time.Hour)

	if _, err := blobClient.SetImmutabilityPolicy(ctx, locked, &blob.SetImmutabilityPolicyOptions{
		Mode: to.Ptr(blob.ImmutabilityPolicySettingLocked),
	}); err != nil {
		t.Fatalf("SetImmutabilityPolicy(locked): %v", err)
	}

	// Shortening a locked policy is rejected.
	if _, err := blobClient.SetImmutabilityPolicy(ctx, fc.Now().Add(1*time.Hour), &blob.SetImmutabilityPolicyOptions{
		Mode: to.Ptr(blob.ImmutabilityPolicySettingLocked),
	}); err == nil {
		t.Fatalf("shortening a locked policy should fail")
	}

	// Deleting a locked policy is rejected.
	if _, err := blobClient.DeleteImmutabilityPolicy(ctx, nil); err == nil {
		t.Fatalf("deleting a locked policy should fail")
	}

	// Extending a locked policy is allowed.
	if _, err := blobClient.SetImmutabilityPolicy(ctx, fc.Now().Add(96*time.Hour), &blob.SetImmutabilityPolicyOptions{
		Mode: to.Ptr(blob.ImmutabilityPolicySettingLocked),
	}); err != nil {
		t.Fatalf("extending a locked policy: %v", err)
	}

	// Delete is still blocked within the (extended) window.
	if _, err := svcClient.DeleteBlob(ctx, "c1", "ledger", nil); !isBlocked(err) {
		t.Fatalf("delete under locked policy = %v, want blocked", err)
	}
}

// TestSDKBlobLegalHold verifies a legal hold blocks delete and overwrite
// independent of any retention window, and lifting it restores mutability.
func TestSDKBlobLegalHold(t *testing.T) {
	ctx := context.Background()

	svcClient, _ := immutabilityFixture(t)

	if _, err := svcClient.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := svcClient.UploadBuffer(ctx, "c1", "held", []byte("evidence"), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	blobClient := svcClient.ServiceClient().NewContainerClient("c1").NewBlobClient("held")

	holdResp, err := blobClient.SetLegalHold(ctx, true, nil)
	if err != nil {
		t.Fatalf("SetLegalHold(true): %v", err)
	}

	if holdResp.LegalHold == nil || !*holdResp.LegalHold {
		t.Fatalf("set legal hold response = %v, want true", holdResp.LegalHold)
	}

	// Under legal hold, delete and overwrite are blocked (no time window applies).
	if _, err := svcClient.DeleteBlob(ctx, "c1", "held", nil); !isBlocked(err) {
		t.Fatalf("delete under legal hold = %v, want blocked", err)
	}

	requireImmutableError(t,
		mustErr(svcClient.UploadBuffer(ctx, "c1", "held", []byte("tamper"), nil)),
		"BlobImmutableDueToLegalHold")

	// Get Blob Properties reports the hold.
	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.LegalHold == nil || !*props.LegalHold {
		t.Fatalf("props legal hold = %v, want true", props.LegalHold)
	}

	// Clearing the hold restores mutability.
	if _, err := blobClient.SetLegalHold(ctx, false, nil); err != nil {
		t.Fatalf("SetLegalHold(false): %v", err)
	}

	if _, err := svcClient.DeleteBlob(ctx, "c1", "held", nil); err != nil {
		t.Fatalf("delete after clearing hold: %v", err)
	}
}

// isBlocked reports whether err is one of the two Azure WORM-block errors.
func isBlocked(err error) bool {
	return bloberror.HasCode(err,
		bloberror.Code("BlobImmutableDueToPolicy"),
		bloberror.Code("BlobImmutableDueToLegalHold"))
}

// mustErr discards a first (response) return value so a one-line call can be fed
// to requireImmutableError.
func mustErr[T any](_ T, err error) error {
	return err
}
