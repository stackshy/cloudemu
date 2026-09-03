// Package gcs_test — suite cell STORAGE / gcp / sdk-compat.
//
// Real cloud.google.com/go/storage SDK journeys for bucket-level IAM
// (Buckets: setIamPolicy/getIamPolicy) against the emulator's GCP HTTP
// server: a fresh bucket reads back an empty-but-valid policy, a set policy
// persists in the default in-memory backend and round-trips on get, a
// stale-etag set is rejected like real GCS's optimistic concurrency, and N
// concurrent setIamPolicy calls starting from the same etag can't all "win"
// (the TOCTOU a separate get-then-set pair would allow).
package gcs_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/iam"
	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
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

// concurrentSetIAMWriters is how many goroutines race to setIamPolicy from
// the same starting etag in TestGCSBucketIAMPolicyConcurrentSetIsAtomic.
const concurrentSetIAMWriters = 10

// TestGCSBucketIAMPolicyConcurrentSetIsAtomic proves the etag precondition
// check and the write happen atomically: N goroutines all submit a
// setIamPolicy built from the SAME starting etag (each with its own distinct
// binding, replacing the whole policy). A separate read-etag-then-write pair
// would let every one of them pass the check before any of them writes,
// silently losing all but the last write applied; the atomic compare-and-set
// must let exactly one of them win and every loser must see a 412.
func TestGCSBucketIAMPolicyConcurrentSetIsAtomic(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	t.Cleanup(func() { _ = client.Close() })

	const bucketName = "iam-concurrent-bucket"

	bkt := client.Bucket(bucketName)
	if cErr := bkt.Create(ctx, e2eProject, nil); cErr != nil {
		t.Fatalf("Create: %v", cErr)
	}

	initial, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (initial): %v", err)
	}

	etag0 := policyEtag(t, initial)
	iamURL := ts.URL + "/storage/v1/b/" + bucketName + "/iam"

	statuses := make([]int, concurrentSetIAMWriters)

	var wg sync.WaitGroup

	for i := range concurrentSetIAMWriters {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			member := fmt.Sprintf("user:writer-%d@example.com", i)
			body := fmt.Sprintf(
				`{"bindings":[{"role":"roles/storage.objectViewer","members":["%s"]}],"etag":%q}`,
				member, etag0,
			)

			req, rErr := http.NewRequestWithContext(ctx, http.MethodPut, iamURL, strings.NewReader(body))
			if rErr != nil {
				t.Errorf("writer %d: NewRequest: %v", i, rErr)
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, dErr := ts.Client().Do(req)
			if dErr != nil {
				t.Errorf("writer %d: Do: %v", i, dErr)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			statuses[i] = resp.StatusCode
		}(i)
	}

	wg.Wait()

	successes := 0

	for i, code := range statuses {
		switch code {
		case http.StatusOK:
			successes++
		case http.StatusPreconditionFailed:
			// expected for every loser
		default:
			t.Errorf("writer %d: unexpected status %d", i, code)
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent setIamPolicy calls from the same etag to "+
			"succeed, got %d — a lost update slipped through the TOCTOU window", concurrentSetIAMWriters, successes)
	}

	final, err := bkt.IAM().Policy(ctx)
	if err != nil {
		t.Fatalf("Policy (final): %v", err)
	}

	members := final.Members("roles/storage.objectViewer")
	if len(members) != 1 {
		t.Fatalf("expected exactly the single winning binding to persist, got %v", members)
	}
}
