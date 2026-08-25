package gcs_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // verifying the GCS md5Hash content digest, not a security primitive
	"errors"
	"hash/crc32"
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
