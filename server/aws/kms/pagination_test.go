package kms_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

// TestSDKListKeysPaginationDeterministic walks ListKeys one key per page and
// asserts every key is seen exactly once. Before the fix the handler paged over
// map-iteration order, so an offset Marker could repeat or skip keys across pages.
func TestSDKListKeysPaginationDeterministic(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	const n = 5
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		out, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
		if err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
		want[aws.ToString(out.KeyMetadata.KeyId)] = true
	}

	seen := map[string]int{}
	var marker *string
	for pages := 0; pages <= n+1; pages++ {
		out, err := c.ListKeys(ctx, &awskms.ListKeysInput{Limit: aws.Int32(1), Marker: marker})
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		for _, k := range out.Keys {
			seen[aws.ToString(k.KeyId)]++
		}
		if !out.Truncated {
			break
		}
		marker = out.NextMarker
	}

	if len(seen) != n {
		t.Fatalf("paginated walk saw %d distinct keys, want %d (%v)", len(seen), n, seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("key %s returned %d times across pages, want exactly 1", id, count)
		}
		if !want[id] {
			t.Fatalf("paginated walk returned unknown key %s", id)
		}
	}
}
