// Package gcs_test — suite cell STORAGE / gcp / sdk-compat.
//
// Real cloud.google.com/go/storage SDK journeys for bucket-level IAM
// (Buckets: setIamPolicy/getIamPolicy) against the emulator's GCP HTTP
// server: a fresh bucket reads back an empty-but-valid policy, a set policy
// persists in the default in-memory backend and round-trips on get, and a
// stale-etag set is rejected like real GCS's optimistic concurrency.
package gcs_test

import (
	"errors"
	"testing"

	"cloud.google.com/go/iam"
	"google.golang.org/api/googleapi"
)

// TestGCSBucketIAMPolicyRoundTrips proves setIamPolicy persists bindings in
// the default in-memory backend (no real-engine extension configured) and
// getIamPolicy reads them back, with the etag changing on every write.
func TestGCSBucketIAMPolicyRoundTrips(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := client.Bucket("iam-bucket")

	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A fresh bucket reads back an empty-but-valid policy, not an error.
	policy, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (fresh bucket): %v", err)
	}

	if roles := policy.Roles(); len(roles) != 0 {
		t.Fatalf("fresh bucket policy should have no bindings, got %v", roles)
	}

	initialEtag := policyEtag(t, policy)
	if initialEtag == "" {
		t.Fatalf("fresh bucket policy should carry a stable etag")
	}

	// setIamPolicy grants a binding.
	policy.Add("allUsers", "roles/storage.objectViewer")

	if err := bkt.IAM().SetPolicy(ctx, policy); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	// getIamPolicy must reflect the binding — this is the persistence bug:
	// without it, the bindings set above would come back empty.
	after, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (after set): %v", err)
	}

	members := after.Members("roles/storage.objectViewer")
	if len(members) != 1 || members[0] != "allUsers" {
		t.Fatalf("binding did not persist: got members %v", members)
	}

	afterEtag := policyEtag(t, after)
	if afterEtag == initialEtag {
		t.Fatalf("etag should change on setIamPolicy, stayed %q", afterEtag)
	}

	// A second update on top of the fresh read succeeds and further changes
	// the etag.
	after.Add("user:alice@example.com", "roles/storage.objectAdmin")

	if err := bkt.IAM().SetPolicy(ctx, after); err != nil {
		t.Fatalf("SetPolicy (second update): %v", err)
	}

	final, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (final): %v", err)
	}

	if members := final.Members("roles/storage.objectAdmin"); len(members) != 1 {
		t.Fatalf("second binding did not persist: got %v", members)
	}

	if members := final.Members("roles/storage.objectViewer"); len(members) != 1 {
		t.Fatalf("first binding should still be present: got %v", members)
	}

	if finalEtag := policyEtag(t, final); finalEtag == afterEtag {
		t.Fatalf("etag should change again, stayed %q", finalEtag)
	}
}

// TestGCSBucketIAMPolicyStaleEtagRejected proves a setIamPolicy built from a
// stale read (someone else changed the policy in between) is rejected with a
// precondition error instead of silently clobbering the newer policy.
func TestGCSBucketIAMPolicyStaleEtagRejected(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := client.Bucket("iam-stale-bucket")

	if err := bkt.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stale, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (stale read): %v", err)
	}

	// Someone else updates the policy first, advancing the etag.
	fresh, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (fresh read): %v", err)
	}

	fresh.Add("allUsers", "roles/storage.objectViewer")

	if err := bkt.IAM().SetPolicy(ctx, fresh); err != nil {
		t.Fatalf("SetPolicy (winning writer): %v", err)
	}

	// The stale writer's set, built from the pre-update etag, must fail.
	stale.Add("user:bob@example.com", "roles/storage.objectAdmin")

	err = bkt.IAM().SetPolicy(ctx, stale)
	if err == nil {
		t.Fatalf("SetPolicy with a stale etag should have failed")
	}

	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
	}

	if gErr.Code != 412 {
		t.Fatalf("expected 412 Precondition Failed, got %d: %v", gErr.Code, gErr)
	}

	// The winning writer's binding must be intact — the stale write must not
	// have partially applied.
	final, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (final): %v", err)
	}

	if members := final.Members("roles/storage.objectViewer"); len(members) != 1 {
		t.Fatalf("winning writer's binding should be intact: got %v", members)
	}

	if members := final.Members("roles/storage.objectAdmin"); len(members) != 0 {
		t.Fatalf("stale writer's binding must not have applied: got %v", members)
	}
}

// policyEtag reaches into the SDK's *iam.Policy via its InternalProto to
// read the wire etag it round-tripped from the server.
func policyEtag(t *testing.T, policy *iam.Policy) string {
	t.Helper()

	if policy.InternalProto == nil {
		t.Fatalf("policy has no InternalProto")
	}

	return string(policy.InternalProto.Etag)
}
