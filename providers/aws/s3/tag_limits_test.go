package s3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// nTags returns a tag set of n distinct tags.
func nTags(n int) map[string]string {
	tags := make(map[string]string, n)
	for i := 0; i < n; i++ {
		tags[fmt.Sprintf("k%d", i)] = "v"
	}

	return tags
}

// assertTagError checks err is a *driver.TagError with the given S3 code that
// also reads as InvalidArgument.
func assertTagError(t *testing.T, err error, code string) {
	t.Helper()

	var te *driver.TagError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want *driver.TagError", err, err)
	}

	if te.Code != code {
		t.Fatalf("TagError.Code = %q, want %q (message %q)", te.Code, code, te.Message)
	}

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("TagError does not read as InvalidArgument: %v", err)
	}
}

func TestValidateObjectTags(t *testing.T) {
	// "𝄞" is one rune but two UTF-16 code units, the unit S3 counts.
	clef := "\U0001D11E"

	tests := []struct {
		name string
		tags map[string]string
		code string // "" means valid
	}{
		{name: "empty", tags: nil},
		{name: "ten tags", tags: nTags(10)},
		{name: "eleven tags", tags: nTags(11), code: "BadRequest"},
		{name: "key 128", tags: map[string]string{strings.Repeat("k", 128): "v"}},
		{name: "key 129", tags: map[string]string{strings.Repeat("k", 129): "v"}, code: "InvalidTag"},
		{name: "value 256", tags: map[string]string{"k": strings.Repeat("v", 256)}},
		{name: "value 257", tags: map[string]string{"k": strings.Repeat("v", 257)}, code: "InvalidTag"},
		{name: "key 64 surrogate pairs", tags: map[string]string{strings.Repeat(clef, 64): "v"}},
		{name: "key 65 surrogate pairs", tags: map[string]string{strings.Repeat(clef, 65): "v"}, code: "InvalidTag"},
		{name: "aws prefix", tags: map[string]string{"aws:created": "v"}, code: "InvalidTag"},
	}

	m := newTestMock()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := m.ValidateObjectTags(tc.tags)
			if tc.code == "" {
				requireNoError(t, err)
				return
			}

			assertTagError(t, err, tc.code)
		})
	}
}

func TestPutObjectTaggingEnforcesLimits(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "k", []byte("x"), "text/plain", nil))
	requireNoError(t, m.PutObjectTagging(ctx, "b", "k", map[string]string{"keep": "me"}))

	assertTagError(t, m.PutObjectTagging(ctx, "b", "k", nTags(11)), "BadRequest")

	// A rejected set leaves the previous tags in place.
	got, err := m.GetObjectTagging(ctx, "b", "k")
	requireNoError(t, err)
	if !maps.Equal(got, map[string]string{"keep": "me"}) {
		t.Fatalf("tags after rejected put = %v, want keep=me", got)
	}
}

func TestMultipartAndCopyTagsEnforceLimits(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	requireNoError(t, m.CreateBucket(ctx, "b"))
	requireNoError(t, m.PutObject(ctx, "b", "src", []byte("x"), "text/plain", nil))

	_, err := m.CreateMultipartUploadWithTagging(ctx, "b", "k", "text/plain", nTags(11))
	assertTagError(t, err, "BadRequest")

	_, err = m.CopyObjectV2(ctx, &driver.CopyObjectRequest{
		DstBucket: "b", DstKey: "dst", Src: driver.CopySource{Bucket: "b", Key: "src"},
		ReplaceTags: true, Tags: map[string]string{"k": strings.Repeat("v", 257)},
	})
	assertTagError(t, err, "InvalidTag")

	if _, err := m.HeadObject(ctx, "b", "dst"); !cerrors.IsNotFound(err) {
		t.Fatalf("rejected copy left a destination object: err=%v", err)
	}
}

func TestCreateBucketWithOptions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tags := map[string]string{"env": "dev", "team": "core"}
	requireNoError(t, m.CreateBucketWithOptions(ctx, "tagged", driver.CreateBucketOptions{Region: "eu-west-1", Tags: tags}))

	got, err := m.GetBucketTagging(ctx, "tagged")
	requireNoError(t, err)
	if !maps.Equal(got, tags) {
		t.Fatalf("bucket tags = %v, want %v", got, tags)
	}

	buckets, err := m.ListBuckets(ctx)
	requireNoError(t, err)
	assertEqual(t, "eu-west-1", buckets[0].Region)

	// An invalid tag set is rejected before the bucket exists.
	err = m.CreateBucketWithOptions(ctx, "bad", driver.CreateBucketOptions{Tags: map[string]string{"aws:x": "y"}})
	assertTagError(t, err, "InvalidTag")

	err = m.CreateBucketWithOptions(ctx, "bad", driver.CreateBucketOptions{Tags: map[string]string{strings.Repeat("k", 129): "y"}})
	assertTagError(t, err, "InvalidTag")

	if m.buckets.Has("bad") {
		t.Fatal("bucket created despite an invalid tag set")
	}

	// No tags behaves like CreateBucketInRegion.
	requireNoError(t, m.CreateBucketWithOptions(ctx, "plain", driver.CreateBucketOptions{}))

	got, err = m.GetBucketTagging(ctx, "plain")
	requireNoError(t, err)
	assertEqual(t, 0, len(got))
}
