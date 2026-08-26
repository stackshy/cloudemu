package s3_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestSDKListObjectsV2CommonPrefixesPagination asserts a delimited ListObjectsV2
// listing caps object keys AND rolled-up common prefixes JOINTLY by MaxKeys over
// one lexicographic stream: every key and every prefix is returned EXACTLY once
// across all pages, and no single page exceeds MaxKeys. Real S3 does not repeat
// CommonPrefixes on every page, and counts them toward MaxKeys.
func TestSDKListObjectsV2CommonPrefixesPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "listv2-cp-page-bucket"
	mustCreateBucket(t, client, bucket)

	// 2000 top-level keys (no delimiter in them) plus 3 keys that roll up into
	// the common prefixes dir0/, dir1/, dir2/. With delimiter "/" and
	// MaxKeys 1000 the listing must span multiple pages.
	const topLevelKeys = 2000
	for i := 0; i < topLevelKeys; i++ {
		key := fmt.Sprintf("key-%05d", i)
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	prefixDirs := []string{"dir0/", "dir1/", "dir2/"}
	for _, d := range prefixDirs {
		key := d + "child"
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	const maxKeys = 1000
	paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int32(maxKeys),
	})

	keyCounts := make(map[string]int)
	prefixCounts := make(map[string]int)
	pages := 0

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		perPage := len(page.Contents) + len(page.CommonPrefixes)
		if perPage > maxKeys {
			t.Fatalf("page %d returned %d items (keys=%d prefixes=%d), exceeds MaxKeys=%d",
				pages, perPage, len(page.Contents), len(page.CommonPrefixes), maxKeys)
		}

		// KeyCount must count keys AND common prefixes on this page.
		if got := int(aws.ToInt32(page.KeyCount)); got != perPage {
			t.Errorf("page %d KeyCount = %d, want %d (keys+prefixes)", pages, got, perPage)
		}

		for _, o := range page.Contents {
			keyCounts[aws.ToString(o.Key)]++
		}

		for _, p := range page.CommonPrefixes {
			prefixCounts[aws.ToString(p.Prefix)]++
		}
	}

	if pages < 2 {
		t.Fatalf("expected the listing to span multiple pages, got %d", pages)
	}

	// Every top-level key returned exactly once.
	if len(keyCounts) != topLevelKeys {
		t.Errorf("distinct keys returned = %d, want %d", len(keyCounts), topLevelKeys)
	}

	for i := 0; i < topLevelKeys; i++ {
		key := fmt.Sprintf("key-%05d", i)
		if keyCounts[key] != 1 {
			t.Errorf("key %s returned %d times, want exactly 1", key, keyCounts[key])
		}
	}

	// Every common prefix returned exactly once (never repeated per page).
	if len(prefixCounts) != len(prefixDirs) {
		t.Errorf("distinct common prefixes = %d, want %d (%v)", len(prefixCounts), len(prefixDirs), prefixCounts)
	}

	for _, d := range prefixDirs {
		if prefixCounts[d] != 1 {
			t.Errorf("common prefix %s returned %d times, want exactly 1", d, prefixCounts[d])
		}
	}
}

// TestSDKListObjectsV2ManyCommonPrefixesTruncate asserts that when the number of
// rolled-up common prefixes alone exceeds MaxKeys, the listing truncates: the
// first page is capped and IsTruncated with a continuation token, and resuming
// returns the remaining prefixes with none repeated or dropped.
func TestSDKListObjectsV2ManyCommonPrefixesTruncate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "listv2-many-cp-bucket"
	mustCreateBucket(t, client, bucket)

	// 1500 distinct directories, each contributing one common prefix under
	// delimiter "/". No top-level (bare) keys, so the page is prefixes-only.
	const dirs = 1500
	for i := 0; i < dirs; i++ {
		key := fmt.Sprintf("d%05d/child", i)
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	const maxKeys = 1000
	page1, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int32(maxKeys),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2 page1: %v", err)
	}

	if len(page1.Contents) != 0 {
		t.Errorf("page1 keys = %d, want 0 (all objects roll up into prefixes)", len(page1.Contents))
	}

	if len(page1.CommonPrefixes) != maxKeys {
		t.Fatalf("page1 common prefixes = %d, want %d (capped by MaxKeys)", len(page1.CommonPrefixes), maxKeys)
	}

	if !aws.ToBool(page1.IsTruncated) {
		t.Fatal("page1 IsTruncated = false, want true (1500 > 1000 prefixes)")
	}

	if aws.ToString(page1.NextContinuationToken) == "" {
		t.Fatal("page1 NextContinuationToken is empty, want a resume token")
	}

	page2, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket:            aws.String(bucket),
		Delimiter:         aws.String("/"),
		MaxKeys:           aws.Int32(maxKeys),
		ContinuationToken: page1.NextContinuationToken,
	})
	if err != nil {
		t.Fatalf("ListObjectsV2 page2: %v", err)
	}

	if len(page2.CommonPrefixes) != dirs-maxKeys {
		t.Errorf("page2 common prefixes = %d, want %d (remaining)", len(page2.CommonPrefixes), dirs-maxKeys)
	}

	if aws.ToBool(page2.IsTruncated) {
		t.Error("page2 IsTruncated = true, want false (remaining prefixes fit)")
	}

	// No prefix appears on both pages, and every directory appears exactly once.
	seen := make(map[string]int)
	for _, p := range page1.CommonPrefixes {
		seen[aws.ToString(p.Prefix)]++
	}

	for _, p := range page2.CommonPrefixes {
		seen[aws.ToString(p.Prefix)]++
	}

	if len(seen) != dirs {
		t.Errorf("distinct prefixes across pages = %d, want %d", len(seen), dirs)
	}

	for i := 0; i < dirs; i++ {
		d := fmt.Sprintf("d%05d/", i)
		if seen[d] != 1 {
			t.Errorf("prefix %s seen %d times across pages, want exactly 1", d, seen[d])
		}
	}
}

// TestSDKListObjectsV2KeysOnlyPaginationUnregressed asserts the non-delimited
// (keys-only) pagination path still returns every key exactly once across pages
// with correct truncation — the CommonPrefixes fix must not disturb it.
func TestSDKListObjectsV2KeysOnlyPaginationUnregressed(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "listv2-keys-only-bucket"
	mustCreateBucket(t, client, bucket)

	const total = 2500
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("obj-%05d", i)
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	const maxKeys = 1000
	paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(maxKeys),
	})

	seen := make(map[string]int)
	pages := 0

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pages++

		if len(page.Contents) > maxKeys {
			t.Fatalf("page %d returned %d keys, exceeds MaxKeys=%d", pages, len(page.Contents), maxKeys)
		}

		if len(page.CommonPrefixes) != 0 {
			t.Errorf("page %d has common prefixes without a delimiter, want none", pages)
		}

		for _, o := range page.Contents {
			seen[aws.ToString(o.Key)]++
		}
	}

	if pages != 3 {
		t.Errorf("pages = %d, want 3 (2500 keys / 1000)", pages)
	}

	if len(seen) != total {
		t.Errorf("distinct keys = %d, want %d", len(seen), total)
	}

	for i := 0; i < total; i++ {
		key := fmt.Sprintf("obj-%05d", i)
		if seen[key] != 1 {
			t.Errorf("key %s returned %d times, want exactly 1", key, seen[key])
		}
	}
}
