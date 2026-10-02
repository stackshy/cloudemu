package loadbalancer_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// gcsBucket is the Cloud Storage bucket the backend buckets in these tests serve.
const gcsBucket = "static-assets"

// newCDNServer serves the LB + Compute handlers with the GCS backend wired, so
// backendBuckets.bucketName is checked against real Cloud Storage buckets.
func newCDNServer(t *testing.T, buckets ...string) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()

	for _, b := range buckets {
		if err := cloudP.GCS.CreateBucket(context.Background(), b); err != nil {
			t.Fatalf("CreateBucket %s: %v", b, err)
		}
	}

	srv := gcpserver.New(gcpserver.Drivers{LB: cloudP.LB, Compute: cloudP.GCE, Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func newBackendBucketsClient(t *testing.T, ts *httptest.Server) *gcpcompute.BackendBucketsClient {
	t.Helper()

	c, err := gcpcompute.NewBackendBucketsRESTClient(context.Background(), clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewBackendBucketsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// bbRef renders a relative backend-bucket reference.
func bbRef(name string) string {
	return "projects/" + testProject + "/global/backendBuckets/" + name
}

// insertBB inserts bb and waits for the operation, failing the test on error.
func insertBB(ctx context.Context, t *testing.T, c *gcpcompute.BackendBucketsClient, bb *computepb.BackendBucket) {
	t.Helper()

	waitOp(ctx, t, "BackendBucket Insert "+bb.GetName(), func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertBackendBucketRequest{Project: testProject, BackendBucketResource: bb})
	})
}

// callOp runs a mutating call and waits for its operation, returning the first error.
func callOp(ctx context.Context, call func() (*gcpcompute.Operation, error)) error {
	op, err := call()
	if err != nil {
		return err
	}

	return op.Wait(ctx)
}

func getBB(ctx context.Context, t *testing.T, c *gcpcompute.BackendBucketsClient, name string) *computepb.BackendBucket {
	t.Helper()

	got, err := c.Get(ctx, &computepb.GetBackendBucketRequest{Project: testProject, BackendBucket: name})
	if err != nil {
		t.Fatalf("BackendBucket Get %s: %v", name, err)
	}

	return got
}

// assertHTTPCode fails unless err is a googleapi.Error carrying code.
func assertHTTPCode(t *testing.T, err error, code int) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error = %v, want a googleapi.Error with code %d", err, code)
	}

	if gerr.Code != code {
		t.Fatalf("error code = %d, want %d (%v)", gerr.Code, code, err)
	}
}

func cdnBucket(name string) *computepb.BackendBucket {
	return &computepb.BackendBucket{
		Name:                  ptrStr(name),
		BucketName:            ptrStr(gcsBucket),
		Description:           ptrStr("static site"),
		EnableCdn:             ptrBool(true),
		CompressionMode:       ptrStr("AUTOMATIC"),
		CustomResponseHeaders: []string{"X-Cache-Status: {cdn_cache_status}"},
		CdnPolicy: &computepb.BackendBucketCdnPolicy{
			CacheMode:               ptrStr("CACHE_ALL_STATIC"),
			DefaultTtl:              ptrI32(60),
			MaxTtl:                  ptrI32(600),
			ClientTtl:               ptrI32(30),
			NegativeCaching:         ptrBool(true),
			SignedUrlCacheMaxAgeSec: func() *int64 { v := int64(7200); return &v }(),
			RequestCoalescing:       ptrBool(true),
		},
	}
}

// TestSDKGCPBackendBucketLifecycle drives insert → get → patch → update →
// setEdgeSecurityPolicy → delete → get(404) through the real BackendBuckets
// client, waiting on every global operation.
func TestSDKGCPBackendBucketLifecycle(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	insertBB(ctx, t, c, cdnBucket("site-bb"))
	assertInsertedBB(t, getBB(ctx, t, c, "site-bb"))

	// PATCH: only members present change; cdnPolicy merges member-by-member.
	waitOp(ctx, t, "Patch", func() (*gcpcompute.Operation, error) {
		return c.Patch(ctx, &computepb.PatchBackendBucketRequest{
			Project: testProject, BackendBucket: "site-bb",
			BackendBucketResource: &computepb.BackendBucket{
				Description: ptrStr("patched"),
				CdnPolicy:   &computepb.BackendBucketCdnPolicy{DefaultTtl: ptrI32(120)},
			},
		})
	})
	assertPatchedBB(t, getBB(ctx, t, c, "site-bb"))

	waitOp(ctx, t, "SetEdgeSecurityPolicy", func() (*gcpcompute.Operation, error) {
		return c.SetEdgeSecurityPolicy(ctx, &computepb.SetEdgeSecurityPolicyBackendBucketRequest{
			Project: testProject, BackendBucket: "site-bb",
			SecurityPolicyReferenceResource: &computepb.SecurityPolicyReference{SecurityPolicy: ptrStr("edge-policy")},
		})
	})

	// PUT: full replace; omitted members are gone, output-only edgeSecurityPolicy stays.
	waitOp(ctx, t, "Update", func() (*gcpcompute.Operation, error) {
		return c.Update(ctx, &computepb.UpdateBackendBucketRequest{
			Project: testProject, BackendBucket: "site-bb",
			BackendBucketResource: &computepb.BackendBucket{Name: ptrStr("site-bb"), BucketName: ptrStr(gcsBucket)},
		})
	})
	assertReplacedBB(t, getBB(ctx, t, c, "site-bb"))

	waitOp(ctx, t, "Delete", func() (*gcpcompute.Operation, error) {
		return c.Delete(ctx, &computepb.DeleteBackendBucketRequest{Project: testProject, BackendBucket: "site-bb"})
	})

	_, err := c.Get(ctx, &computepb.GetBackendBucketRequest{Project: testProject, BackendBucket: "site-bb"})
	assertHTTPCode(t, err, 404)

	_, err = c.Delete(ctx, &computepb.DeleteBackendBucketRequest{Project: testProject, BackendBucket: "site-bb"})
	assertHTTPCode(t, err, 404)
}

func assertInsertedBB(t *testing.T, got *computepb.BackendBucket) {
	t.Helper()

	if got.GetKind() != "compute#backendBucket" || got.GetId() == 0 || got.GetCreationTimestamp() == "" {
		t.Errorf("identity: kind=%q id=%d created=%q", got.GetKind(), got.GetId(), got.GetCreationTimestamp())
	}

	if !strings.HasSuffix(got.GetSelfLink(), "/compute/v1/"+bbRef("site-bb")) {
		t.Errorf("selfLink = %q", got.GetSelfLink())
	}

	if got.GetBucketName() != gcsBucket || !got.GetEnableCdn() || got.GetCompressionMode() != "AUTOMATIC" ||
		got.GetDescription() != "static site" || len(got.GetCustomResponseHeaders()) != 1 {
		t.Errorf("fields did not round-trip: %v", got)
	}

	p := got.GetCdnPolicy()
	if p.GetCacheMode() != "CACHE_ALL_STATIC" || p.GetDefaultTtl() != 60 || p.GetMaxTtl() != 600 ||
		p.GetClientTtl() != 30 || !p.GetNegativeCaching() || p.GetSignedUrlCacheMaxAgeSec() != 7200 ||
		!p.GetRequestCoalescing() {
		t.Errorf("cdnPolicy did not round-trip: %v", p)
	}
}

func assertPatchedBB(t *testing.T, got *computepb.BackendBucket) {
	t.Helper()

	if got.GetDescription() != "patched" {
		t.Errorf("description = %q, want patched", got.GetDescription())
	}

	if got.GetBucketName() != gcsBucket || !got.GetEnableCdn() || len(got.GetCustomResponseHeaders()) != 1 {
		t.Errorf("patch clobbered members it did not name: %v", got)
	}

	p := got.GetCdnPolicy()
	if p.GetDefaultTtl() != 120 || p.GetCacheMode() != "CACHE_ALL_STATIC" || p.GetMaxTtl() != 600 {
		t.Errorf("cdnPolicy after patch = %v, want defaultTtl=120 with cacheMode/maxTtl kept", p)
	}
}

func assertReplacedBB(t *testing.T, got *computepb.BackendBucket) {
	t.Helper()

	if got.Description != nil || got.EnableCdn != nil || got.CdnPolicy != nil || len(got.GetCustomResponseHeaders()) != 0 {
		t.Errorf("update did not replace the resource: %v", got)
	}

	if got.GetEdgeSecurityPolicy() != "edge-policy" {
		t.Errorf("edgeSecurityPolicy = %q, want edge-policy (output-only, kept across update)", got.GetEdgeSecurityPolicy())
	}
}

// TestSDKGCPBackendBucketListPaging lists with maxResults=1 (the iterator
// follows nextPageToken) and with a name filter.
func TestSDKGCPBackendBucketListPaging(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	for _, n := range []string{"bb-c", "bb-a", "bb-b"} {
		insertBB(ctx, t, c, &computepb.BackendBucket{Name: ptrStr(n), BucketName: ptrStr(gcsBucket)})
	}

	all := listBBNames(ctx, t, c, &computepb.ListBackendBucketsRequest{Project: testProject, MaxResults: func() *uint32 {
		v := uint32(1)
		return &v
	}()})
	if strings.Join(all, ",") != "bb-a,bb-b,bb-c" {
		t.Errorf("paged list = %v, want [bb-a bb-b bb-c]", all)
	}

	if pages := countBBPages(t, c.List(ctx, &computepb.ListBackendBucketsRequest{Project: testProject})); pages != 3 {
		t.Errorf("pages at page size 1 = %d, want 3 (server must honor maxResults + pageToken)", pages)
	}

	filtered := listBBNames(ctx, t, c, &computepb.ListBackendBucketsRequest{Project: testProject, Filter: ptrStr("name = bb-b")})
	if strings.Join(filtered, ",") != "bb-b" {
		t.Errorf("filtered list = %v, want [bb-b]", filtered)
	}
}

// countBBPages walks it one item per page, returning how many pages the server served.
func countBBPages(t *testing.T, it *gcpcompute.BackendBucketIterator) int {
	t.Helper()

	pager := iterator.NewPager(it, 1, "")

	for pages := 0; ; pages++ {
		var page []*computepb.BackendBucket

		token, err := pager.NextPage(&page)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		if len(page) > 1 {
			t.Fatalf("page holds %d items, want <= 1", len(page))
		}

		if token == "" {
			return pages + 1
		}
	}
}

func listBBNames(ctx context.Context, t *testing.T, c *gcpcompute.BackendBucketsClient, req *computepb.ListBackendBucketsRequest) []string {
	t.Helper()

	var names []string

	it := c.List(ctx, req)

	for {
		bb, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return names
		}

		if err != nil {
			t.Fatalf("List: %v", err)
		}

		names = append(names, bb.GetName())
	}
}

// TestSDKGCPBackendBucketInsertValidation covers the insert-time 400s and the
// duplicate 409.
func TestSDKGCPBackendBucketInsertValidation(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	withPolicy := func(p *computepb.BackendBucketCdnPolicy) *computepb.BackendBucket {
		return &computepb.BackendBucket{Name: ptrStr("bad-bb"), BucketName: ptrStr(gcsBucket), CdnPolicy: p}
	}

	cases := map[string]*computepb.BackendBucket{
		"missing bucketName": {Name: ptrStr("bad-bb")},
		"unknown GCS bucket": {Name: ptrStr("bad-bb"), BucketName: ptrStr("no-such-bucket")},
		"non-RFC1035 name":   {Name: ptrStr("Bad_Name"), BucketName: ptrStr(gcsBucket)},
		"compressionMode":    {Name: ptrStr("bad-bb"), BucketName: ptrStr(gcsBucket), CompressionMode: ptrStr("ZSTD")},
		"cacheMode":          withPolicy(&computepb.BackendBucketCdnPolicy{CacheMode: ptrStr("BOGUS")}),
		"defaultTtl>maxTtl":  withPolicy(&computepb.BackendBucketCdnPolicy{DefaultTtl: ptrI32(900), MaxTtl: ptrI32(60)}),
		"maxTtl over 1 year": withPolicy(&computepb.BackendBucketCdnPolicy{MaxTtl: ptrI32(31622401)}),
		"negative clientTtl": withPolicy(&computepb.BackendBucketCdnPolicy{ClientTtl: ptrI32(-1)}),
		"negativeCachingPolicy without negativeCaching": withPolicy(&computepb.BackendBucketCdnPolicy{
			NegativeCachingPolicy: []*computepb.BackendBucketCdnPolicyNegativeCachingPolicy{{Code: ptrI32(404), Ttl: ptrI32(60)}},
		}),
	}

	for name, bb := range cases {
		t.Run(name, func(t *testing.T) {
			err := callOp(ctx, func() (*gcpcompute.Operation, error) {
				return c.Insert(ctx, &computepb.InsertBackendBucketRequest{Project: testProject, BackendBucketResource: bb})
			})
			assertHTTPCode(t, err, 400)
		})
	}

	insertBB(ctx, t, c, &computepb.BackendBucket{Name: ptrStr("dup-bb"), BucketName: ptrStr(gcsBucket)})

	err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Insert(ctx, &computepb.InsertBackendBucketRequest{Project: testProject,
			BackendBucketResource: &computepb.BackendBucket{Name: ptrStr("dup-bb"), BucketName: ptrStr(gcsBucket)}})
	})
	assertHTTPCode(t, err, 409)

	_, err = c.Get(ctx, &computepb.GetBackendBucketRequest{Project: testProject, BackendBucket: "ghost-bb"})
	assertHTTPCode(t, err, 404)
}

// TestSDKGCPBackendBucketMutationValidation covers patch/update 400s (checked
// against the merged result) and that a rejected change leaves the record as it was.
func TestSDKGCPBackendBucketMutationValidation(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	insertBB(ctx, t, c, cdnBucket("site-bb"))

	patch := func(bb *computepb.BackendBucket) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return c.Patch(ctx, &computepb.PatchBackendBucketRequest{
				Project: testProject, BackendBucket: "site-bb", BackendBucketResource: bb,
			})
		})
	}

	// defaultTtl alone is in range, but exceeds the stored maxTtl (600) once merged.
	assertHTTPCode(t, patch(&computepb.BackendBucket{CdnPolicy: &computepb.BackendBucketCdnPolicy{DefaultTtl: ptrI32(900)}}), 400)
	assertHTTPCode(t, patch(&computepb.BackendBucket{BucketName: ptrStr("no-such-bucket")}), 400)
	assertHTTPCode(t, patch(&computepb.BackendBucket{CdnPolicy: &computepb.BackendBucketCdnPolicy{CacheMode: ptrStr("NOPE")}}), 400)

	err := callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Update(ctx, &computepb.UpdateBackendBucketRequest{
			Project: testProject, BackendBucket: "site-bb",
			BackendBucketResource: &computepb.BackendBucket{Name: ptrStr("site-bb")},
		})
	})
	assertHTTPCode(t, err, 400)

	assertInsertedBB(t, getBB(ctx, t, c, "site-bb"))

	assertHTTPCode(t, callOp(ctx, func() (*gcpcompute.Operation, error) {
		return c.Patch(ctx, &computepb.PatchBackendBucketRequest{
			Project: testProject, BackendBucket: "ghost-bb",
			BackendBucketResource: &computepb.BackendBucket{Description: ptrStr("x")},
		})
	}), 404)
}

// TestSDKGCPURLMapBackendBucketReferences proves url-map references to backend
// buckets are validated (defaultService, pathMatchers[].defaultService,
// pathRules[].service; full URL or relative path) and that a referenced backend
// bucket cannot be deleted until the url-map is gone.
func TestSDKGCPURLMapBackendBucketReferences(t *testing.T) {
	ts := newCDNServer(t, gcsBucket)
	ctx := context.Background()
	bb := newBackendBucketsClient(t, ts)

	um, err := gcpcompute.NewUrlMapsRESTClient(ctx, clientOpts(ts)...)
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = um.Close() })

	insertMap := func(m *computepb.UrlMap) error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return um.Insert(ctx, &computepb.InsertUrlMapRequest{Project: testProject, UrlMapResource: m})
		})
	}

	assertInvalidArgument(t, insertMap(&computepb.UrlMap{Name: ptrStr("cdn-map"), DefaultService: ptrStr(bbRef("ghost-bb"))}))

	insertBB(ctx, t, bb, &computepb.BackendBucket{Name: ptrStr("real-bb"), BucketName: ptrStr(gcsBucket)})

	assertInvalidArgument(t, insertMap(&computepb.UrlMap{
		Name: ptrStr("cdn-map"), DefaultService: ptrStr(bbRef("real-bb")),
		PathMatchers: []*computepb.PathMatcher{{
			Name: ptrStr("pm"), DefaultService: ptrStr(bbRef("real-bb")),
			PathRules: []*computepb.PathRule{{Paths: []string{"/img/*"}, Service: ptrStr(bbRef("ghost-bb"))}},
		}},
	}))

	fullURL := ts.URL + "/compute/v1/" + bbRef("real-bb")
	if err := insertMap(&computepb.UrlMap{
		Name: ptrStr("cdn-map"), DefaultService: ptrStr(fullURL),
		PathMatchers: []*computepb.PathMatcher{{
			Name: ptrStr("pm"), DefaultService: ptrStr(bbRef("real-bb")),
			PathRules: []*computepb.PathRule{{Paths: []string{"/img/*"}, Service: ptrStr(bbRef("real-bb"))}},
		}},
	}); err != nil {
		t.Fatalf("UrlMap Insert referencing an existing backend bucket: %v", err)
	}

	deleteBB := func() error {
		return callOp(ctx, func() (*gcpcompute.Operation, error) {
			return bb.Delete(ctx, &computepb.DeleteBackendBucketRequest{Project: testProject, BackendBucket: "real-bb"})
		})
	}

	assertResourceInUse(t, deleteBB())

	waitOp(ctx, t, "UrlMap Delete", func() (*gcpcompute.Operation, error) {
		return um.Delete(ctx, &computepb.DeleteUrlMapRequest{Project: testProject, UrlMap: "cdn-map"})
	})

	if err := deleteBB(); err != nil {
		t.Fatalf("Delete after url-map removed: %v", err)
	}
}

// TestSDKGCPBackendBucketWithoutStorage: with no GCS backend wired, bucketName is
// only required to be present.
func TestSDKGCPBackendBucketWithoutStorage(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()
	c := newBackendBucketsClient(t, ts)

	insertBB(ctx, t, c, &computepb.BackendBucket{Name: ptrStr("any-bb"), BucketName: ptrStr("unchecked-bucket")})

	if got := getBB(ctx, t, c, "any-bb"); got.GetBucketName() != "unchecked-bucket" {
		t.Errorf("bucketName = %q", got.GetBucketName())
	}
}
