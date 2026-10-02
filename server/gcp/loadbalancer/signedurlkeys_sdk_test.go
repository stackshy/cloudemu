package loadbalancer_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// signingKey is a valid 128-bit base64url signed URL key value. The tests
// assert it never appears in a response.
const signingKey = "nZtRohdNF9m3cKM24IcK4w=="

// TestSDKGCPBackendBucketSignedURLKeys drives backendBuckets.addSignedUrlKey
// and deleteSignedUrlKey through the real BackendBucketsClient (Terraform's
// google_compute_backend_bucket_signed_url_key uses the same calls): the key
// name is listed under cdnPolicy.signedUrlKeyNames, the value is never echoed,
// a patch keeps the names, and bad requests are refused.
func TestSDKGCPBackendBucketSignedURLKeys(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	insertBB(ctx, t, c, &computepb.BackendBucket{Name: ptrStr("signed-bb"), BucketName: ptrStr(gcsBucket), EnableCdn: ptrBool(true)})

	add := func(bucket, name, value string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.AddSignedUrlKey(ctx, &computepb.AddSignedUrlKeyBackendBucketRequest{
				Project: testProject, BackendBucket: bucket,
				SignedUrlKeyResource: &computepb.SignedUrlKey{KeyName: ptrStr(name), KeyValue: ptrStr(value)},
			})
		})
	}

	del := func(name string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.DeleteSignedUrlKey(ctx, &computepb.DeleteSignedUrlKeyBackendBucketRequest{
				Project: testProject, BackendBucket: "signed-bb", KeyName: name,
			})
		})
	}

	for _, name := range []string{"key-a", "key-b"} {
		if err := add("signed-bb", name, signingKey); err != nil {
			t.Fatalf("AddSignedUrlKey %s: %v", name, err)
		}
	}

	if got := getBB(ctx, t, c, "signed-bb").GetCdnPolicy().GetSignedUrlKeyNames(); !slices.Equal(got, []string{"key-a", "key-b"}) {
		t.Fatalf("signedUrlKeyNames = %v, want [key-a key-b]", got)
	}

	_, raw := doJSON(t, ts, "GET", ts.URL+"/compute/v1/projects/"+testProject+"/global/backendBuckets/signed-bb", "")
	if strings.Contains(raw, signingKey) {
		t.Fatalf("GET echoes the signed URL key value: %s", raw)
	}

	assertHTTPCode(t, add("signed-bb", "key-a", signingKey), 409)
	assertHTTPCode(t, add("signed-bb", "key-c", "not-base64!"), 400)
	assertHTTPCode(t, add("signed-bb", "key-c", "c2hvcnQ="), 400)
	assertHTTPCode(t, add("signed-bb", "Bad_Name", signingKey), 400)
	assertHTTPCode(t, add("ghost-bb", "key-c", signingKey), 404)

	if err := add("signed-bb", "key-c", signingKey); err != nil {
		t.Fatalf("AddSignedUrlKey key-c: %v", err)
	}

	assertHTTPCode(t, add("signed-bb", "key-d", signingKey), 400)

	// A patch that touches cdnPolicy (and even echoes a bogus key list) keeps
	// the names: they are output-only.
	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Patch(ctx, &computepb.PatchBackendBucketRequest{
			Project: testProject, BackendBucket: "signed-bb",
			BackendBucketResource: &computepb.BackendBucket{CdnPolicy: &computepb.BackendBucketCdnPolicy{
				DefaultTtl: ptrI32(60), SignedUrlKeyNames: []string{"forged"},
			}},
		})
	}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := del("key-b"); err != nil {
		t.Fatalf("DeleteSignedUrlKey: %v", err)
	}

	assertHTTPCode(t, del("key-b"), 404)

	if got := getBB(ctx, t, c, "signed-bb").GetCdnPolicy().GetSignedUrlKeyNames(); !slices.Equal(got, []string{"key-a", "key-c"}) {
		t.Fatalf("signedUrlKeyNames after patch + delete = %v, want [key-a key-c]", got)
	}
}

// TestSDKGCPBackendServiceSignedURLKeys: the same pair on backendServices
// (405 before), with the names kept across a cdnPolicy patch.
func TestSDKGCPBackendServiceSignedURLKeys(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	c := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	insertBS(ctx, t, c, "signed-bs")

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.AddSignedUrlKey(ctx, &computepb.AddSignedUrlKeyBackendServiceRequest{
			Project: testProject, BackendService: "signed-bs",
			SignedUrlKeyResource: &computepb.SignedUrlKey{KeyName: ptrStr("key-a"), KeyValue: ptrStr(signingKey)},
		})
	}); err != nil {
		t.Fatalf("AddSignedUrlKey: %v", err)
	}

	if err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Patch(ctx, &computepb.PatchBackendServiceRequest{
			Project: testProject, BackendService: "signed-bs",
			BackendServiceResource: &computepb.BackendService{CdnPolicy: &computepb.BackendServiceCdnPolicy{
				CacheMode: ptrStr("CACHE_ALL_STATIC"),
			}},
		})
	}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := c.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "signed-bs"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if names := got.GetCdnPolicy().GetSignedUrlKeyNames(); !slices.Equal(names, []string{"key-a"}) {
		t.Fatalf("signedUrlKeyNames = %v, want [key-a]", names)
	}

	_, raw := doJSON(t, ts, "GET", ts.URL+"/compute/v1/projects/"+testProject+"/global/backendServices/signed-bs", "")
	if strings.Contains(raw, signingKey) {
		t.Fatalf("GET echoes the signed URL key value: %s", raw)
	}

	del := func(name string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.DeleteSignedUrlKey(ctx, &computepb.DeleteSignedUrlKeyBackendServiceRequest{
				Project: testProject, BackendService: "signed-bs", KeyName: name,
			})
		})
	}

	if err := del("key-a"); err != nil {
		t.Fatalf("DeleteSignedUrlKey: %v", err)
	}

	assertHTTPCode(t, del("key-a"), 404)

	got, err = c.Get(ctx, &computepb.GetBackendServiceRequest{Project: testProject, BackendService: "signed-bs"})
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}

	if names := got.GetCdnPolicy().GetSignedUrlKeyNames(); len(names) != 0 {
		t.Fatalf("signedUrlKeyNames after delete = %v, want none", names)
	}
}

// TestSDKGCPURLMapInvalidateCache drives urlMaps.invalidateCache through the
// real UrlMapsClient: it returns an operation that completes, and is refused
// for a missing url map or a path that does not start with "/".
func TestSDKGCPURLMapInvalidateCache(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	bb := newBackendBucketsClient(t, ts)

	um, err := gcpcompute.NewUrlMapsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = um.Close() })

	insertBB(ctx, t, bb, &computepb.BackendBucket{Name: ptrStr("site-bb"), BucketName: ptrStr(gcsBucket)})
	waitOp(ctx, t, "UrlMap Insert", func() (*gcpcompute.Operation, error) {
		return um.Insert(ctx, &computepb.InsertUrlMapRequest{Project: testProject, UrlMapResource: &computepb.UrlMap{
			Name: ptrStr("cdn-map"), DefaultService: ptrStr(bbRef("site-bb")),
		}})
	})

	invalidate := func(m, path string) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return um.InvalidateCache(ctx, &computepb.InvalidateCacheUrlMapRequest{
				Project: testProject, UrlMap: m,
				CacheInvalidationRuleResource: &computepb.CacheInvalidationRule{Path: ptrStr(path)},
			})
		})
	}

	if err := invalidate("cdn-map", "/images/*"); err != nil {
		t.Fatalf("InvalidateCache: %v", err)
	}

	assertHTTPCode(t, invalidate("ghost-map", "/images/*"), 404)
	assertHTTPCode(t, invalidate("cdn-map", "images"), 400)
}
