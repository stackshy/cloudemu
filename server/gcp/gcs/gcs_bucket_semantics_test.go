package gcs_test

import (
	"testing"

	"cloud.google.com/go/storage"
)

// TestGCSBucketLocationStorageClass proves a bucket created with a non-default
// location/storage class reads them back (instead of a hardcoded US/STANDARD)
// so IaC tools don't see perpetual drift.
func TestGCSBucketLocationStorageClass(t *testing.T) {
	ctx, client := newStorageClient(t)

	bkt := client.Bucket("eu-nearline")
	if err := bkt.Create(ctx, e2eProject, &storage.BucketAttrs{Location: "EU", StorageClass: "NEARLINE"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if a.Location != "EU" {
		t.Errorf("Location = %q, want EU", a.Location)
	}

	if a.StorageClass != "NEARLINE" {
		t.Errorf("StorageClass = %q, want NEARLINE", a.StorageClass)
	}

	if a.LocationType != "multi-region" {
		t.Errorf("LocationType = %q, want multi-region", a.LocationType)
	}
}

// TestGCSBucketLifecyclePersists proves a lifecycle rule set via Update is
// persisted and read back, instead of being accepted-and-dropped.
func TestGCSBucketLifecyclePersists(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "lifecycle-cfg")

	lc := storage.Lifecycle{Rules: []storage.LifecycleRule{{
		Action:    storage.LifecycleAction{Type: storage.DeleteAction},
		Condition: storage.LifecycleCondition{AgeInDays: 30},
	}}}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{Lifecycle: &lc}); err != nil {
		t.Fatalf("update lifecycle: %v", err)
	}

	a, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if len(a.Lifecycle.Rules) != 1 {
		t.Fatalf("lifecycle rules = %d, want 1", len(a.Lifecycle.Rules))
	}

	rule := a.Lifecycle.Rules[0]
	if rule.Action.Type != storage.DeleteAction {
		t.Errorf("action = %q, want Delete", rule.Action.Type)
	}

	if rule.Condition.AgeInDays != 30 {
		t.Errorf("age = %d, want 30", rule.Condition.AgeInDays)
	}
}

// TestGCSBucketIAM proves bucket IAM (get/set/testPermissions) is served
// (previously a 404), so google_storage_bucket_iam_* works.
func TestGCSBucketIAM(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "iam-cfg")

	handle := bkt.IAM()

	policy, err := handle.Policy(ctx)
	if err != nil {
		t.Fatalf("IAM().Policy failed (was 404 before the fix): %v", err)
	}

	policy.Add("user:alice@example.com", "roles/storage.objectViewer")

	if err := handle.SetPolicy(ctx, policy); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	got, err := handle.Policy(ctx)
	if err != nil {
		t.Fatalf("Policy after set: %v", err)
	}

	if !got.HasRole("user:alice@example.com", "roles/storage.objectViewer") {
		t.Errorf("IAM policy did not round-trip the binding: %+v", got)
	}

	perms, err := handle.TestPermissions(ctx, []string{"storage.objects.get"})
	if err != nil {
		t.Fatalf("TestPermissions: %v", err)
	}

	if len(perms) != 1 || perms[0] != "storage.objects.get" {
		t.Errorf("TestPermissions = %v, want [storage.objects.get]", perms)
	}
}

// TestGCSBucketMetageneration proves buckets carry a non-zero metageneration
// that increases on each configuration change (was metageneration=0 before).
func TestGCSBucketMetageneration(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "metagen")

	a, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if a.MetaGeneration < 1 {
		t.Errorf("MetaGeneration = %d, want >= 1", a.MetaGeneration)
	}

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true}); err != nil {
		t.Fatalf("update: %v", err)
	}

	a2, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after update: %v", err)
	}

	if a2.MetaGeneration <= a.MetaGeneration {
		t.Errorf("MetaGeneration did not increase after a patch: %d -> %d", a.MetaGeneration, a2.MetaGeneration)
	}
}
