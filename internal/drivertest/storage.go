// Package drivertest holds cross-provider conformance suites: one shared
// acceptance test per driver interface (services/*/driver), run against every
// provider's implementation of it. This is the fstest.TestFS pattern — the
// interface's behavioral contract is encoded exactly once here, and each
// provider's own _test.go package calls the matching Run*Conformance function
// against a freshly constructed driver instance.
//
// Only genuinely provider-agnostic behavior belongs here — anything an
// individual cloud's wire protocol adds beyond the shared driver interface
// (Azure blob leases, S3 object versioning, GCP-only quirks, ...) stays in
// that provider's own tests, not in a conformance suite every provider must
// satisfy identically.
package drivertest

import (
	"bytes"
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// contentTypeText is the content-type used by tests that don't exercise
// content-type behavior directly, kept as one constant rather than a
// repeated literal.
const contentTypeText = "text/plain"

// Expected counts for the fixed fixtures each test below builds.
const (
	wantBucketCount          = 2
	wantAllObjectsCount      = 3
	wantPrefixedObjectsCount = 2
	wantCommonPrefixesCount  = 2
)

// RunBucketConformance runs the shared behavioral contract of
// storage/driver.Bucket against a freshly constructed driver instance,
// obtained by calling newDriver. newDriver is called once per subtest so
// each one starts from an empty, isolated backend.
func RunBucketConformance(t *testing.T, newDriver func() storagedriver.Bucket) {
	t.Helper()

	t.Run("CreateBucket", func(t *testing.T) { testCreateBucket(t, newDriver()) })
	t.Run("DeleteBucket", func(t *testing.T) { testDeleteBucket(t, newDriver()) })
	t.Run("ListBuckets", func(t *testing.T) { testListBuckets(t, newDriver()) })
	t.Run("ObjectLifecycle", func(t *testing.T) { testObjectLifecycle(t, newDriver()) })
	t.Run("ListObjects", func(t *testing.T) { testListObjects(t, newDriver()) })
	t.Run("ListObjectsPagination", func(t *testing.T) { testListObjectsPagination(t, newDriver()) })
	t.Run("CopyObject", func(t *testing.T) { testCopyObject(t, newDriver()) })
	t.Run("HeadObject", func(t *testing.T) { testHeadObject(t, newDriver()) })
}

// testCreateBucket covers CreateBucket's shared contract: an empty name is
// rejected, a fresh name succeeds, and a duplicate name is AlreadyExists.
func testCreateBucket(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	if err := d.CreateBucket(ctx, ""); !cerrors.IsInvalidArgument(err) {
		t.Errorf("CreateBucket(empty name): want InvalidArgument, got %v", err)
	}

	mustCreateBucket(t, d, "bucket-a")

	if err := d.CreateBucket(ctx, "bucket-a"); !cerrors.IsAlreadyExists(err) {
		t.Errorf("CreateBucket(duplicate): want AlreadyExists, got %v", err)
	}
}

// testDeleteBucket covers DeleteBucket's shared contract: a missing bucket is
// NotFound, a non-empty bucket is FailedPrecondition, and an empty bucket
// deletes cleanly and is no longer listed.
func testDeleteBucket(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()
	assertNotFound(t, d.DeleteBucket(ctx, "nonexistent"), "DeleteBucket(missing)")

	mustCreateBucket(t, d, "full")
	requireNoError(t, d.PutObject(ctx, "full", "key", []byte("data"), contentTypeText, nil), "PutObject")

	if err := d.DeleteBucket(ctx, "full"); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("DeleteBucket(non-empty): want FailedPrecondition, got %v", err)
	}

	requireNoError(t, d.DeleteObject(ctx, "full", "key"), "DeleteObject")
	requireNoError(t, d.DeleteBucket(ctx, "full"), "DeleteBucket(now empty)")

	buckets, err := d.ListBuckets(ctx)
	requireNoError(t, err, "ListBuckets")

	for _, b := range buckets {
		if b.Name == "full" {
			t.Errorf("ListBuckets: deleted bucket %q still present", b.Name)
		}
	}
}

// testListBuckets covers ListBuckets' shared contract: an empty backend lists
// nothing, and created buckets are all reported back (order is not part of
// the contract, so this only checks set membership).
func testListBuckets(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	empty, err := d.ListBuckets(ctx)
	requireNoError(t, err, "ListBuckets(empty)")

	if len(empty) != 0 {
		t.Errorf("ListBuckets(empty): want 0 buckets, got %d", len(empty))
	}

	mustCreateBucket(t, d, "beta")
	mustCreateBucket(t, d, "alpha")

	buckets, err := d.ListBuckets(ctx)
	requireNoError(t, err, "ListBuckets")

	if len(buckets) != wantBucketCount {
		t.Fatalf("ListBuckets: want %d buckets, got %d", wantBucketCount, len(buckets))
	}

	seen := map[string]bool{}
	for _, b := range buckets {
		seen[b.Name] = true
	}

	for _, name := range []string{"alpha", "beta"} {
		if !seen[name] {
			t.Errorf("ListBuckets: missing bucket %q", name)
		}
	}
}

// testObjectLifecycle covers Put/Get/Delete's shared contract: reads and
// writes against a missing bucket or a missing key are NotFound, a stored
// object round-trips its data/content-type/metadata/size, and a deleted
// object is no longer gettable.
func testObjectLifecycle(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()
	putErr := d.PutObject(ctx, "nosuchbucket", "key", []byte("data"), contentTypeText, nil)
	assertNotFound(t, putErr, "PutObject(missing bucket)")

	mustCreateBucket(t, d, "b1")
	testObjectMissingErrors(t, d)
	testObjectRoundTrip(t, d)
}

// testObjectMissingErrors covers the NotFound cases of Get/Delete against an
// existing bucket "b1" but an absent key, and against a bucket that doesn't
// exist at all.
func testObjectMissingErrors(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	_, getErr := d.GetObject(ctx, "b1", "missing-key")
	assertNotFound(t, getErr, "GetObject(missing key)")

	_, getBucketErr := d.GetObject(ctx, "nosuchbucket", "key")
	assertNotFound(t, getBucketErr, "GetObject(missing bucket)")

	assertNotFound(t, d.DeleteObject(ctx, "b1", "missing-key"), "DeleteObject(missing key)")
}

// testObjectRoundTrip covers a Put followed by a Get that returns matching
// data/content-type/size/metadata, followed by a Delete that makes the
// object NotFound again — all against the existing bucket "b1".
func testObjectRoundTrip(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()
	data := []byte("hello world")
	meta := map[string]string{"owner": "conformance"}

	requireNoError(t, d.PutObject(ctx, "b1", "greeting.txt", data, contentTypeText, meta), "PutObject")

	obj, err := d.GetObject(ctx, "b1", "greeting.txt")
	requireNoError(t, err, "GetObject")

	if !bytes.Equal(obj.Data, data) {
		t.Errorf("GetObject: data = %q, want %q", obj.Data, data)
	}

	if obj.Info.ContentType != contentTypeText {
		t.Errorf("GetObject: ContentType = %q, want %q", obj.Info.ContentType, contentTypeText)
	}

	if obj.Info.Size != int64(len(data)) {
		t.Errorf("GetObject: Size = %d, want %d", obj.Info.Size, len(data))
	}

	if obj.Info.Metadata["owner"] != "conformance" {
		t.Errorf("GetObject: Metadata[owner] = %q, want %q", obj.Info.Metadata["owner"], "conformance")
	}

	requireNoError(t, d.DeleteObject(ctx, "b1", "greeting.txt"), "DeleteObject")

	_, err = d.GetObject(ctx, "b1", "greeting.txt")
	assertNotFound(t, err, "GetObject(after delete)")
}

// testHeadObject covers HeadObject's shared contract: it reports the same
// size/key as the stored object without an error, and NotFound for a missing
// bucket or key.
func testHeadObject(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	mustCreateBucket(t, d, "b1")

	data := []byte("hello")
	requireNoError(t, d.PutObject(ctx, "b1", "file.txt", data, contentTypeText, nil), "PutObject")

	info, err := d.HeadObject(ctx, "b1", "file.txt")
	requireNoError(t, err, "HeadObject")

	if info.Key != "file.txt" {
		t.Errorf("HeadObject: Key = %q, want %q", info.Key, "file.txt")
	}

	if info.Size != int64(len(data)) {
		t.Errorf("HeadObject: Size = %d, want %d", info.Size, len(data))
	}

	_, missingKeyErr := d.HeadObject(ctx, "b1", "missing")
	assertNotFound(t, missingKeyErr, "HeadObject(missing key)")

	_, missingBucketErr := d.HeadObject(ctx, "nosuchbucket", "file.txt")
	assertNotFound(t, missingBucketErr, "HeadObject(missing bucket)")
}

// testListObjects covers ListObjects' shared contract: an unfiltered listing
// returns every object, a Prefix filters by key prefix, a Delimiter groups
// keys past the prefix into CommonPrefixes instead of returning them as
// objects, and a missing bucket errors.
func testListObjects(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	mustCreateBucket(t, d, "b1")

	for _, key := range []string{"docs/a.txt", "docs/b.txt", "images/c.jpg"} {
		requireNoError(t, d.PutObject(ctx, "b1", key, []byte("x"), "", nil), "PutObject("+key+")")
	}

	all, err := d.ListObjects(ctx, "b1", storagedriver.ListOptions{})
	requireNoError(t, err, "ListObjects(all)")

	if len(all.Objects) != wantAllObjectsCount {
		t.Errorf("ListObjects(all): got %d objects, want %d", len(all.Objects), wantAllObjectsCount)
	}

	prefixed, err := d.ListObjects(ctx, "b1", storagedriver.ListOptions{Prefix: "docs/"})
	requireNoError(t, err, "ListObjects(prefix)")

	if len(prefixed.Objects) != wantPrefixedObjectsCount {
		t.Errorf("ListObjects(prefix=docs/): got %d objects, want %d", len(prefixed.Objects), wantPrefixedObjectsCount)
	}

	testListObjectsDelimiter(t, d)

	_, err = d.ListObjects(ctx, "nosuchbucket", storagedriver.ListOptions{})
	if err == nil {
		t.Error("ListObjects(missing bucket): want error, got nil")
	}
}

// testListObjectsDelimiter covers the Delimiter behavior in isolation: keys
// past the prefix collapse into CommonPrefixes rather than being returned as
// objects. Assumes bucket "b1" already holds the docs/images fixture from
// testListObjects.
func testListObjectsDelimiter(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	delimited, err := d.ListObjects(context.Background(), "b1", storagedriver.ListOptions{Delimiter: "/"})
	requireNoError(t, err, "ListObjects(delimiter)")

	if len(delimited.Objects) != 0 {
		t.Errorf("ListObjects(delimiter=/): got %d objects, want 0", len(delimited.Objects))
	}

	if len(delimited.CommonPrefixes) != wantCommonPrefixesCount {
		t.Errorf(
			"ListObjects(delimiter=/): got %d common prefixes, want %d", len(delimited.CommonPrefixes), wantCommonPrefixesCount,
		)
	}
}

// testListObjectsPagination covers the pagination basics ListOptions
// guarantees: MaxKeys caps a page and sets IsTruncated with a usable
// NextPageToken, and following the token pages exhaustively through every
// object with no duplicates and no omissions, ending in a final,
// non-truncated page.
func testListObjectsPagination(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	mustCreateBucket(t, d, "b1")

	const (
		total      = 5
		pageSize   = 2
		maxRounds  = total + 1
		fixtureKey = "key-"
	)

	want := make(map[string]bool, total)

	for i := range total {
		key := fixtureKey + string(rune('a'+i))
		requireNoError(t, d.PutObject(ctx, "b1", key, []byte("x"), "", nil), "PutObject("+key+")")

		want[key] = true
	}

	got := collectPaginated(t, d, "b1", pageSize, maxRounds)

	if len(got) != len(want) {
		t.Fatalf("pagination: collected %d objects across pages, want %d", len(got), len(want))
	}

	for key := range want {
		if !got[key] {
			t.Errorf("pagination: key %q never returned across any page", key)
		}
	}
}

// collectPaginated walks every page of bucket via ListObjects with the given
// page size, following NextPageToken until a non-truncated page, and returns
// the set of keys seen. It fails the test on a duplicate key, a truncated
// page with no token, or exceeding maxRounds (a runaway pagination loop).
func collectPaginated(t *testing.T, d storagedriver.Bucket, bucket string, pageSize, maxRounds int) map[string]bool {
	t.Helper()

	ctx := context.Background()
	got := map[string]bool{}
	token := ""

	for page := range maxRounds {
		result, err := d.ListObjects(ctx, bucket, storagedriver.ListOptions{MaxKeys: pageSize, PageToken: token})
		requireNoError(t, err, "ListObjects(page)")

		for i := range result.Objects {
			key := result.Objects[i].Key
			if got[key] {
				t.Errorf("ListObjects(page %d): key %q returned more than once", page, key)
			}

			got[key] = true
		}

		if !result.IsTruncated {
			if result.NextPageToken != "" {
				t.Errorf("ListObjects(page %d): NextPageToken set on a non-truncated result", page)
			}

			return got
		}

		if result.NextPageToken == "" {
			t.Fatalf("ListObjects(page %d): IsTruncated true but NextPageToken empty", page)
		}

		token = result.NextPageToken
	}

	t.Fatalf("pagination did not terminate within %d pages", maxRounds)

	return got
}

// testCopyObject covers CopyObject's shared contract: a copy round-trips the
// source's data and content-type to the destination key, and a missing
// source or destination bucket errors.
func testCopyObject(t *testing.T, d storagedriver.Bucket) {
	t.Helper()

	ctx := context.Background()

	mustCreateBucket(t, d, "src")
	mustCreateBucket(t, d, "dst")

	data := []byte("copy me")
	requireNoError(t, d.PutObject(ctx, "src", "original.txt", data, contentTypeText, nil), "PutObject")

	src := storagedriver.CopySource{Bucket: "src", Key: "original.txt"}
	requireNoError(t, d.CopyObject(ctx, "dst", "copied.txt", src), "CopyObject")

	obj, err := d.GetObject(ctx, "dst", "copied.txt")
	requireNoError(t, err, "GetObject(copy destination)")

	if !bytes.Equal(obj.Data, data) {
		t.Errorf("CopyObject: destination data = %q, want %q", obj.Data, data)
	}

	if obj.Info.ContentType != contentTypeText {
		t.Errorf("CopyObject: destination ContentType = %q, want %q", obj.Info.ContentType, contentTypeText)
	}

	missingSrc := storagedriver.CopySource{Bucket: "nosuchbucket", Key: "x"}
	assertNotFound(t, d.CopyObject(ctx, "dst", "x", missingSrc), "CopyObject(missing source bucket)")

	missingKey := storagedriver.CopySource{Bucket: "src", Key: "nosuchkey"}
	assertNotFound(t, d.CopyObject(ctx, "dst", "x", missingKey), "CopyObject(missing source key)")

	assertNotFound(t, d.CopyObject(ctx, "nosuchbucket", "x", src), "CopyObject(missing destination bucket)")
}

// mustCreateBucket creates a bucket and fails the test immediately if the
// driver rejects it, so setup errors surface at the right line instead of
// cascading into unrelated assertion failures.
func mustCreateBucket(t *testing.T, d storagedriver.Bucket, name string) {
	t.Helper()
	requireNoError(t, d.CreateBucket(context.Background(), name), "CreateBucket("+name+")")
}

// requireNoError fails the test immediately (t.Fatalf) if err is non-nil,
// naming the failing operation.
func requireNoError(t *testing.T, err error, what string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
}

// assertNotFound reports (t.Errorf, non-fatal) unless err is a NotFound
// error, naming the failing operation.
func assertNotFound(t *testing.T, err error, what string) {
	t.Helper()

	if !cerrors.IsNotFound(err) {
		t.Errorf("%s: want NotFound, got %v", what, err)
	}
}
