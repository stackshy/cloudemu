package cloudlogging_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
	logging "google.golang.org/api/logging/v2"
)

// TestSDKBucketsLifecycle guards create/get/list/patch/delete of a
// user-defined log bucket, including a dup create and a get of a missing one.
func TestSDKBucketsLifecycle(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject + "/locations/global"
	bucketName := parent + "/buckets/my-bucket"

	created, err := svc.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{
		Description:   "app logs",
		RetentionDays: 14,
	}).BucketId("my-bucket").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Create: %v", err)
	}

	if created.Name != bucketName {
		t.Errorf("created bucket name = %q, want %q", created.Name, bucketName)
	}

	if created.RetentionDays != 14 {
		t.Errorf("created retentionDays = %d, want 14", created.RetentionDays)
	}

	if created.LifecycleState != "ACTIVE" {
		t.Errorf("created lifecycleState = %q, want ACTIVE", created.LifecycleState)
	}

	if _, err := svc.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{}).
		BucketId("my-bucket").Context(ctx).Do(); err == nil {
		t.Fatal("Buckets.Create(dup): want error, got nil")
	}

	got, err := svc.Projects.Locations.Buckets.Get(bucketName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Get: %v", err)
	}

	if got.Description != "app logs" {
		t.Errorf("get description = %q, want %q", got.Description, "app logs")
	}

	list, err := svc.Projects.Locations.Buckets.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.List: %v", err)
	}

	// _Default and _Required are auto-provisioned alongside the user bucket.
	if len(list.Buckets) != 3 {
		t.Fatalf("Buckets.List = %d buckets, want 3 (_Default, _Required, my-bucket): %+v",
			len(list.Buckets), list.Buckets)
	}

	updated, err := svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		RetentionDays: 30,
	}).UpdateMask("retentionDays").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Patch(retentionDays): %v", err)
	}

	if updated.RetentionDays != 30 {
		t.Errorf("updated retentionDays = %d, want 30", updated.RetentionDays)
	}

	if updated.Description != "app logs" {
		t.Errorf("patch with a narrow mask clobbered description: %q", updated.Description)
	}

	if _, err := svc.Projects.Locations.Buckets.Delete(bucketName).Context(ctx).Do(); err != nil {
		t.Fatalf("Buckets.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Buckets.Get(bucketName).Context(ctx).Do(); err == nil {
		t.Fatal("Buckets.Get(deleted): want error, got nil")
	}
}

// TestSDKBucketsPatchNoMaskPreservesDescription guards a mask-less Patch (the
// Go SDK does not require .UpdateMask()): a caller touching only
// retentionDays must not silently wipe the existing description. This
// exercises updateBucket's presence-heuristic fallback, which previously set
// SetDescription unconditionally to true regardless of whether the body
// actually carried a description.
func TestSDKBucketsPatchNoMaskPreservesDescription(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject + "/locations/global"
	bucketName := parent + "/buckets/no-mask-bucket"

	if _, err := svc.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{
		Description:   "important",
		RetentionDays: 14,
	}).BucketId("no-mask-bucket").Context(ctx).Do(); err != nil {
		t.Fatalf("Buckets.Create: %v", err)
	}

	// No .UpdateMask() call: only RetentionDays is set on the body.
	updated, err := svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		RetentionDays: 30,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Patch(no mask): %v", err)
	}

	if updated.RetentionDays != 30 {
		t.Errorf("updated retentionDays = %d, want 30", updated.RetentionDays)
	}

	if updated.Description != "important" {
		t.Errorf("mask-less patch wiped description: got %q, want %q", updated.Description, "important")
	}

	got, err := svc.Projects.Locations.Buckets.Get(bucketName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Get: %v", err)
	}

	if got.Description != "important" {
		t.Errorf("get description after mask-less patch = %q, want %q", got.Description, "important")
	}
}

// TestSDKBucketsLockedGuards guards the locked-bucket invariants: retention
// cannot be reduced, the bucket cannot be unlocked, and it cannot be deleted.
func TestSDKBucketsLockedGuards(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject + "/locations/global"
	bucketName := parent + "/buckets/locked-bucket"

	if _, err := svc.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{
		RetentionDays: 60,
	}).BucketId("locked-bucket").Context(ctx).Do(); err != nil {
		t.Fatalf("Buckets.Create: %v", err)
	}

	locked, err := svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		Locked: true,
	}).UpdateMask("locked").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Patch(locked): %v", err)
	}

	if !locked.Locked {
		t.Fatal("bucket did not report locked=true after locking")
	}

	_, err = svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		RetentionDays: 10,
	}).UpdateMask("retentionDays").Context(ctx).Do()
	assertConflict(t, "Patch(reduce retention on locked bucket)", err)

	_, err = svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		Locked: false,
	}).UpdateMask("locked").Context(ctx).Do()
	assertConflict(t, "Patch(unlock a locked bucket)", err)

	// Raising retention on a locked bucket is still allowed.
	raised, err := svc.Projects.Locations.Buckets.Patch(bucketName, &logging.LogBucket{
		RetentionDays: 90,
	}).UpdateMask("retentionDays").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Patch(raise retention on locked bucket): %v", err)
	}

	if raised.RetentionDays != 90 {
		t.Errorf("raised retentionDays = %d, want 90", raised.RetentionDays)
	}

	_, err = svc.Projects.Locations.Buckets.Delete(bucketName).Context(ctx).Do()
	assertConflict(t, "Delete(locked bucket)", err)
}

// TestSDKBucketsSpecialBuckets guards the _Default/_Required auto-provisioned
// buckets: neither can be deleted, _Required cannot be modified at all, and
// creating a bucket under either reserved id is rejected.
func TestSDKBucketsSpecialBuckets(t *testing.T) {
	svc := newLoggingService(t)
	ctx := context.Background()

	parent := "projects/" + testProject + "/locations/global"

	def, err := svc.Projects.Locations.Buckets.Get(parent + "/buckets/_Default").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Get(_Default): %v", err)
	}

	if def.RetentionDays != 30 {
		t.Errorf("_Default retentionDays = %d, want 30", def.RetentionDays)
	}

	required, err := svc.Projects.Locations.Buckets.Get(parent + "/buckets/_Required").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Buckets.Get(_Required): %v", err)
	}

	if required.RetentionDays != 400 {
		t.Errorf("_Required retentionDays = %d, want 400", required.RetentionDays)
	}

	if !required.Locked {
		t.Error("_Required is not reported as locked")
	}

	if _, err := svc.Projects.Locations.Buckets.Delete(parent + "/buckets/_Default").Context(ctx).Do(); err == nil {
		t.Fatal("Buckets.Delete(_Default): want error, got nil")
	}

	if _, err := svc.Projects.Locations.Buckets.Delete(parent + "/buckets/_Required").Context(ctx).Do(); err == nil {
		t.Fatal("Buckets.Delete(_Required): want error, got nil")
	}

	_, err = svc.Projects.Locations.Buckets.Patch(parent+"/buckets/_Required", &logging.LogBucket{
		RetentionDays: 500,
	}).UpdateMask("retentionDays").Context(ctx).Do()
	assertConflict(t, "Patch(_Required)", err)

	if _, err := svc.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{}).
		BucketId("_Default").Context(ctx).Do(); err == nil {
		t.Fatal("Buckets.Create(_Default): want error, got nil")
	}
}

// assertConflict fails t unless err is a googleapi.Error with a 409 status —
// the mapping for both AlreadyExists and FailedPrecondition (see gcprest.WriteCErr).
func assertConflict(t *testing.T, op string, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: want error, got nil", op)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("%s: err = %v, want *googleapi.Error", op, err)
	}

	if gerr.Code != http.StatusConflict {
		t.Fatalf("%s: status = %d, want %d", op, gerr.Code, http.StatusConflict)
	}
}
