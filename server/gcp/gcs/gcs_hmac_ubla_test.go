// Package gcs_test — suite cell STORAGE / gcp / sdk-compat.
//
// Real cloud.google.com/go/storage SDK journeys for service-account HMAC keys
// (Projects.hmacKeys) and settable Uniform Bucket-Level Access + Public Access
// Prevention (bucket iamConfiguration), driven against the emulator's GCP HTTP
// server.
package gcs_test

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

const hmacServiceAccount = "svc@e2e-project.iam.gserviceaccount.com"

// TestGCSHMACKeyLifecycle walks the full real-user HMAC journey:
// create -> list -> get -> deactivate -> delete, and proves an ACTIVE key
// cannot be deleted while an INACTIVE one can.
func TestGCSHMACKeyLifecycle(t *testing.T) {
	ctx, client := newStorageClient(t)

	key, err := client.CreateHMACKey(ctx, e2eProject, hmacServiceAccount)
	if err != nil {
		t.Fatalf("CreateHMACKey: %v", err)
	}

	if key.AccessID == "" {
		t.Fatal("CreateHMACKey returned empty AccessID")
	}

	if key.Secret == "" {
		t.Fatal("CreateHMACKey returned empty Secret (must be surfaced once at create)")
	}

	if key.State != storage.Active {
		t.Errorf("new key State = %q, want ACTIVE", key.State)
	}

	if key.ServiceAccountEmail != hmacServiceAccount {
		t.Errorf("ServiceAccountEmail = %q, want %q", key.ServiceAccountEmail, hmacServiceAccount)
	}

	// List reflects the created key.
	assertHMACKeyListed(t, ctx, client, key.AccessID)

	handle := client.HMACKeyHandle(e2eProject, key.AccessID)

	got, err := handle.Get(ctx)
	if err != nil {
		t.Fatalf("HMACKeyHandle.Get: %v", err)
	}

	if got.Secret != "" {
		t.Error("Get must not return the secret")
	}

	if got.AccessID != key.AccessID {
		t.Errorf("Get AccessID = %q, want %q", got.AccessID, key.AccessID)
	}

	// Deleting an ACTIVE key is rejected.
	if delErr := handle.Delete(ctx); delErr == nil {
		t.Fatal("Delete of an ACTIVE key succeeded, want error")
	} else {
		assertHTTPStatus(t, delErr, 400)
	}

	// Deactivate, then delete succeeds.
	updated, err := handle.Update(ctx, storage.HMACKeyAttrsToUpdate{State: storage.Inactive})
	if err != nil {
		t.Fatalf("Update to INACTIVE: %v", err)
	}

	if updated.State != storage.Inactive {
		t.Errorf("updated State = %q, want INACTIVE", updated.State)
	}

	if delErr := handle.Delete(ctx); delErr != nil {
		t.Fatalf("Delete of an INACTIVE key: %v", delErr)
	}

	// Gone.
	if _, getErr := handle.Get(ctx); getErr == nil {
		t.Fatal("Get after delete succeeded, want not-found")
	} else {
		assertHTTPStatus(t, getErr, 404)
	}
}

func assertHMACKeyListed(t *testing.T, ctx context.Context, client *storage.Client, accessID string) {
	t.Helper()

	it := client.ListHMACKeys(ctx, e2eProject)

	for {
		md, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("ListHMACKeys: %v", err)
		}

		if md.AccessID == accessID {
			return
		}
	}

	t.Fatalf("access id %q not found in ListHMACKeys", accessID)
}

// TestGCSHMACKeyServiceAccountRequired proves a create with no service account
// is rejected.
func TestGCSHMACKeyServiceAccountRequired(t *testing.T) {
	ctx, client := newStorageClient(t)

	if _, err := client.CreateHMACKey(ctx, e2eProject, ""); err == nil {
		t.Fatal("CreateHMACKey with empty service account succeeded, want error")
	}
}

// TestGCSUniformBucketLevelAccessRoundTrips proves UBLA enabled via a bucket
// patch is persisted and read back with its lockedTime — instead of being
// dropped by a hardcoded iamConfiguration.
func TestGCSUniformBucketLevelAccessRoundTrips(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "ubla-bucket")

	before, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if before.UniformBucketLevelAccess.Enabled {
		t.Fatal("UBLA enabled by default, want disabled")
	}

	if _, err = bkt.Update(ctx, storage.BucketAttrsToUpdate{
		UniformBucketLevelAccess: &storage.UniformBucketLevelAccess{Enabled: true},
	}); err != nil {
		t.Fatalf("enable UBLA: %v", err)
	}

	after, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after enable: %v", err)
	}

	if !after.UniformBucketLevelAccess.Enabled {
		t.Fatal("UBLA not enabled after update")
	}

	if after.UniformBucketLevelAccess.LockedTime.IsZero() {
		t.Error("UBLA LockedTime is zero, want a lock window when enabled")
	}

	// Disabling clears it.
	if _, err = bkt.Update(ctx, storage.BucketAttrsToUpdate{
		UniformBucketLevelAccess: &storage.UniformBucketLevelAccess{Enabled: false},
	}); err != nil {
		t.Fatalf("disable UBLA: %v", err)
	}

	off, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after disable: %v", err)
	}

	if off.UniformBucketLevelAccess.Enabled {
		t.Error("UBLA still enabled after disable")
	}

	if !off.UniformBucketLevelAccess.LockedTime.IsZero() {
		t.Error("UBLA LockedTime not cleared after disable")
	}
}

// TestGCSPublicAccessPreventionRoundTrips proves publicAccessPrevention is
// settable and read back (default inherited).
func TestGCSPublicAccessPreventionRoundTrips(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "pap-bucket")

	def, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if def.PublicAccessPrevention != storage.PublicAccessPreventionInherited &&
		def.PublicAccessPrevention != storage.PublicAccessPreventionUnknown {
		t.Errorf("default PublicAccessPrevention = %v, want inherited/unknown", def.PublicAccessPrevention)
	}

	if _, err = bkt.Update(ctx, storage.BucketAttrsToUpdate{
		PublicAccessPrevention: storage.PublicAccessPreventionEnforced,
	}); err != nil {
		t.Fatalf("set PAP enforced: %v", err)
	}

	after, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs after PAP: %v", err)
	}

	if after.PublicAccessPrevention != storage.PublicAccessPreventionEnforced {
		t.Errorf("PublicAccessPrevention = %v, want enforced", after.PublicAccessPrevention)
	}
}

func assertHTTPStatus(t *testing.T, err error, want int) {
	t.Helper()

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a *googleapi.Error", err)
	}

	if apiErr.Code != want {
		t.Errorf("HTTP status = %d, want %d (err: %v)", apiErr.Code, want, err)
	}
}
