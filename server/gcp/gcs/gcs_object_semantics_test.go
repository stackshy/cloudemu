package gcs_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // verifying the GCS md5Hash content digest, not a security primitive
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

// assertPreconditionFailed asserts err is a 412 conditionNotMet from GCS.
func assertPreconditionFailed(t *testing.T, err error, when string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected a 412 precondition error, got nil", when)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("%s: expected *googleapi.Error, got %T: %v", when, err, err)
	}

	if gerr.Code != http.StatusPreconditionFailed {
		t.Errorf("%s: got HTTP %d, want 412", when, gerr.Code)
	}
}

// TestGCSObjectInsertPreconditions proves ifGenerationMatch is honored: a
// create-if-absent (DoesNotExist) succeeds once but 412s over an existing
// object, and GenerationMatch to a non-existent generation 412s — the behaviors
// distributed locks and create-if-absent rely on.
func TestGCSObjectInsertPreconditions(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "precond")

	w := bkt.Object("k").If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	if _, err := w.Write([]byte("v1")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("first create-if-absent should succeed: %v", err)
	}

	w2 := bkt.Object("k").If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w2.Write([]byte("v2"))
	assertPreconditionFailed(t, w2.Close(), "DoesNotExist over an existing object")

	w3 := bkt.Object("k").If(storage.Conditions{GenerationMatch: 999999}).NewWriter(ctx)
	_, _ = w3.Write([]byte("v3"))
	assertPreconditionFailed(t, w3.Close(), "GenerationMatch:999999")

	if got := readObject(t, ctx, bkt, "k"); string(got) != "v1" {
		t.Errorf("object mutated despite failed preconditions: got %q, want v1", got)
	}
}

// TestGCSObjectGenerationMinted proves each write mints a fresh, non-zero
// generation instead of the old hardcoded "1", so optimistic concurrency and
// versioning can distinguish writes.
func TestGCSObjectGenerationMinted(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "gen")

	putObject(t, ctx, bkt, "k", "text/plain", []byte("one"), nil)

	a1, err := bkt.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("first Attrs: %v", err)
	}

	putObject(t, ctx, bkt, "k", "text/plain", []byte("two"), nil)

	a2, err := bkt.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("second Attrs: %v", err)
	}

	if a1.Generation == 0 || a2.Generation == 0 {
		t.Fatalf("generation must be non-zero: first=%d second=%d", a1.Generation, a2.Generation)
	}

	if a1.Generation == a2.Generation {
		t.Errorf("overwrite kept generation %d; real GCS mints a new one", a1.Generation)
	}
}

// TestGCSObjectChecksums proves Attrs returns both md5Hash and crc32c — the
// crc32c the Go client uses for download integrity.
func TestGCSObjectChecksums(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "sums")

	data := []byte("hello world")
	putObject(t, ctx, bkt, "k", "text/plain", data, nil)

	a, err := bkt.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	wantMD5 := md5.Sum(data) //nolint:gosec // content digest, not security
	if !bytes.Equal(a.MD5, wantMD5[:]) {
		t.Errorf("MD5 = %x, want %x", a.MD5, wantMD5)
	}

	wantCRC := crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))
	if a.CRC32C != wantCRC {
		t.Errorf("CRC32C = %d, want %d", a.CRC32C, wantCRC)
	}
}

// TestGCSObjectUpdateMetadata proves Objects: patch/update mutates contentType,
// cacheControl, and custom metadata (previously a 405) and bumps metageneration.
func TestGCSObjectUpdateMetadata(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "patch")

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v"), map[string]string{"a": "1"})

	got, err := bkt.Object("k").Update(ctx, storage.ObjectAttrsToUpdate{
		ContentType:  "application/json",
		CacheControl: "public, max-age=60",
		Metadata:     map[string]string{"a": "1", "b": "2"},
	})
	if err != nil {
		t.Fatalf("Object.Update (patch) failed: %v", err)
	}

	if got.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", got.ContentType)
	}

	if got.CacheControl != "public, max-age=60" {
		t.Errorf("CacheControl = %q, want public, max-age=60", got.CacheControl)
	}

	if got.Metadata["b"] != "2" {
		t.Errorf("Metadata = %v, want b=2", got.Metadata)
	}

	if got.Metageneration < 2 {
		t.Errorf("Metageneration = %d, want >= 2 after an update", got.Metageneration)
	}
}

// TestGCSObjectCompose proves Objects: compose concatenates source bytes into a
// destination (previously a 405).
func TestGCSObjectCompose(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "compose")

	putObject(t, ctx, bkt, "p1", "text/plain", []byte("foo"), nil)
	putObject(t, ctx, bkt, "p2", "text/plain", []byte("bar"), nil)

	comp := bkt.Object("combined").ComposerFrom(bkt.Object("p1"), bkt.Object("p2"))
	comp.ContentType = "text/plain"

	if _, err := comp.Run(ctx); err != nil {
		t.Fatalf("compose failed: %v", err)
	}

	if got := readObject(t, ctx, bkt, "combined"); string(got) != "foobar" {
		t.Errorf("composed object = %q, want foobar", got)
	}
}

// TestGCSObjectVersionsList proves a versioning-enabled bucket retains prior
// generations, so a Versions=true list returns every generation of a key.
func TestGCSObjectVersionsList(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "versions")

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v1"), nil)
	putObject(t, ctx, bkt, "k", "text/plain", []byte("v2"), nil)

	it := bkt.Objects(ctx, &storage.Query{Versions: true})

	var gens []int64

	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("versioned list: %v", err)
		}

		if a.Name == "k" {
			gens = append(gens, a.Generation)
		}
	}

	if len(gens) != 2 {
		t.Fatalf("versioned list returned %d generations for k, want 2: %v", len(gens), gens)
	}

	if gens[0] == gens[1] {
		t.Errorf("two retained generations share id %d", gens[0])
	}
}

// TestGCSObjectDeletePrecondition proves a delete with a mismatched
// ifGenerationMatch is rejected 412 and leaves the object untouched — the
// optimistic-concurrency delete real GCS enforces.
func TestGCSObjectDeletePrecondition(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "delprecond")

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v1"), nil)

	err := bkt.Object("k").If(storage.Conditions{GenerationMatch: 999999}).Delete(ctx)
	assertPreconditionFailed(t, err, "Delete with mismatched GenerationMatch")

	if got := readObject(t, ctx, bkt, "k"); string(got) != "v1" {
		t.Errorf("object deleted despite failed precondition: got %q, want v1", got)
	}
}

// TestGCSVersionedDeleteRetainsGenerations proves a live delete on a
// versioning-enabled bucket archives the current generation (it becomes
// noncurrent) rather than dropping it — so a Versions=true list still returns
// every prior generation after the live object is gone.
func TestGCSVersionedDeleteRetainsGenerations(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "verdel")

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v1"), nil)
	putObject(t, ctx, bkt, "k", "text/plain", []byte("v2"), nil)

	if err := bkt.Object("k").Delete(ctx); err != nil {
		t.Fatalf("delete live object: %v", err)
	}

	// Live read must now 404 — the object has no current generation.
	if _, err := bkt.Object("k").Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("live Attrs after delete = %v, want ErrObjectNotExist", err)
	}

	var gens []int64

	it := bkt.Objects(ctx, &storage.Query{Versions: true})

	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("versioned list: %v", err)
		}

		if a.Name == "k" {
			gens = append(gens, a.Generation)
		}
	}

	if len(gens) != 2 {
		t.Fatalf("after live delete, versioned list returned %d generations, want 2 noncurrent: %v", len(gens), gens)
	}
}

// TestGCSInsertSystemProperties proves cacheControl/contentEncoding/
// contentDisposition/contentLanguage set at INSERT persist and read back on
// Attrs (previously dropped, fixing google_storage_bucket_object drift).
func TestGCSInsertSystemProperties(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "sysprops")

	w := bkt.Object("k").NewWriter(ctx)
	w.ContentType = "text/plain"
	w.CacheControl = "public, max-age=3600"
	w.ContentEncoding = "gzip"
	w.ContentDisposition = "attachment; filename=\"f.txt\""
	w.ContentLanguage = "en"

	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	a, err := bkt.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if a.CacheControl != "public, max-age=3600" {
		t.Errorf("CacheControl = %q, want public, max-age=3600", a.CacheControl)
	}

	if a.ContentEncoding != "gzip" {
		t.Errorf("ContentEncoding = %q, want gzip", a.ContentEncoding)
	}

	if a.ContentDisposition != "attachment; filename=\"f.txt\"" {
		t.Errorf("ContentDisposition = %q, want attachment...", a.ContentDisposition)
	}

	if a.ContentLanguage != "en" {
		t.Errorf("ContentLanguage = %q, want en", a.ContentLanguage)
	}
}

// TestGCSGenerationAddressedRead proves ?generation reads the addressed
// revision's bytes, not the current ones — a versioned bucket keeps prior
// generations readable by id.
func TestGCSGenerationAddressedRead(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "genread")

	if _, err := bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v1"), nil)

	a1, err := bkt.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("first Attrs: %v", err)
	}

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v2"), nil)

	if got := readObject(t, ctx, bkt, "k"); string(got) != "v2" {
		t.Fatalf("live read = %q, want v2", got)
	}

	rd, err := bkt.Object("k").Generation(a1.Generation).NewReader(ctx)
	if err != nil {
		t.Fatalf("generation-addressed NewReader: %v", err)
	}
	defer rd.Close()

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read gen1: %v", err)
	}

	if string(got) != "v1" {
		t.Errorf("generation(%d) read = %q, want v1", a1.Generation, got)
	}
}

// TestGCSConditionalReadMismatch proves a conditional read with a mismatched
// ifGenerationMatch is rejected 412.
func TestGCSConditionalReadMismatch(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "condread")

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v1"), nil)

	_, err := bkt.Object("k").If(storage.Conditions{GenerationMatch: 123456}).NewReader(ctx)
	assertPreconditionFailed(t, err, "conditional read with mismatched GenerationMatch")

	_, err = bkt.Object("k").If(storage.Conditions{MetagenerationMatch: 999}).Attrs(ctx)
	assertPreconditionFailed(t, err, "conditional Attrs with mismatched MetagenerationMatch")
}

// TestGCSUpdatePrecondition proves an Objects: patch with a mismatched
// ifMetagenerationMatch is rejected 412.
func TestGCSUpdatePrecondition(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "updprecond")

	putObject(t, ctx, bkt, "k", "text/plain", []byte("v"), nil)

	_, err := bkt.Object("k").If(storage.Conditions{MetagenerationMatch: 999}).Update(ctx, storage.ObjectAttrsToUpdate{
		ContentType: "application/json",
	})
	assertPreconditionFailed(t, err, "Update with mismatched MetagenerationMatch")
}

// TestGCSComposePrecondition proves a compose with DoesNotExist over an already
// existing destination is rejected 412.
func TestGCSComposePrecondition(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "compprecond")

	putObject(t, ctx, bkt, "p1", "text/plain", []byte("foo"), nil)
	putObject(t, ctx, bkt, "p2", "text/plain", []byte("bar"), nil)
	putObject(t, ctx, bkt, "dst", "text/plain", []byte("existing"), nil)

	comp := bkt.Object("dst").If(storage.Conditions{DoesNotExist: true}).ComposerFrom(bkt.Object("p1"), bkt.Object("p2"))

	_, err := comp.Run(ctx)
	assertPreconditionFailed(t, err, "compose DoesNotExist over existing destination")

	if got := readObject(t, ctx, bkt, "dst"); string(got) != "existing" {
		t.Errorf("destination overwritten despite failed precondition: got %q", got)
	}
}

// TestGCSObjectStorageClass proves an object inherits its bucket's default
// storage class (NEARLINE) instead of a hardcoded STANDARD.
func TestGCSObjectStorageClass(t *testing.T) {
	ctx, client := newStorageClient(t)

	b := client.Bucket("nearline")
	if err := b.Create(ctx, e2eProject, &storage.BucketAttrs{StorageClass: "NEARLINE"}); err != nil {
		t.Fatalf("create NEARLINE bucket: %v", err)
	}

	putObject(t, ctx, b, "k", "text/plain", []byte("v"), nil)

	a, err := b.Object("k").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs: %v", err)
	}

	if a.StorageClass != "NEARLINE" {
		t.Errorf("StorageClass = %q, want NEARLINE (bucket default)", a.StorageClass)
	}
}
