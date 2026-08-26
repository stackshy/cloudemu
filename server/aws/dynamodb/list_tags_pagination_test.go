// list_tags_pagination_test.go — unit coverage for the ListTagsOfResource
// pagination plumbing. DynamoDB caps a resource at 50 tags, so a real response
// fits one page; these tests drive pageTags with a deliberately small page size
// to prove the offset-token cursor walks a stable-sorted order exactly once
// (no duplicate, no skip), terminates, and rejects a malformed token.
package dynamodb

import "testing"

func TestPageTagsWalksAllPagesStable(t *testing.T) {
	// Deliberately unsorted so the result order cannot depend on input order.
	tags := []tagJSON{
		{Key: "e", Value: "5"}, {Key: "a", Value: "1"}, {Key: "d", Value: "4"},
		{Key: "b", Value: "2"}, {Key: "c", Value: "3"},
	}

	const pageSize = 2

	var (
		order []string
		token string
		pages int
	)

	for {
		page, err := pageTags(tags, token, pageSize)
		if err != nil {
			t.Fatalf("pageTags: %v", err)
		}

		if len(page.Items) > pageSize {
			t.Fatalf("page has %d items, want <= %d", len(page.Items), pageSize)
		}

		for _, tg := range page.Items {
			order = append(order, tg.Key)
		}

		pages++
		if page.NextPageToken == "" {
			break
		}

		token = page.NextPageToken

		if pages > len(tags) {
			t.Fatal("pagination did not terminate")
		}
	}

	want := []string{"a", "b", "c", "d", "e"}
	if len(order) != len(want) {
		t.Fatalf("walked %d keys, want %d (%v)", len(order), len(want), order)
	}

	for i, k := range want {
		if order[i] != k {
			t.Fatalf("key at %d = %q, want %q (order %v)", i, order[i], k, order)
		}
	}
}

func TestPageTagsInvalidTokenErrors(t *testing.T) {
	if _, err := pageTags([]tagJSON{{Key: "a"}}, "!!not-base64!!", listTagsPageSize); err == nil {
		t.Fatal("want error for malformed token, got nil")
	}
}
