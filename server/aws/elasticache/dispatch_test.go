package elasticache_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/elasticache"
)

// TestMatchesScopeGatesSharedTagActions verifies the shared tag verbs
// (AddTagsToResource / ListTagsForResource / RemoveTagsFromResource), which
// ElastiCache shares with RDS and SNS on the query wire, are only claimed when
// the SigV4 credential scope names "elasticache". Otherwise ElastiCache would
// swallow those calls and answer with a CacheClusterNotFound error.
func TestMatchesScopeGatesSharedTagActions(t *testing.T) {
	h := elasticache.New(nil)

	const (
		snsScope   = "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/sns/aws4_request, Signature=x"
		cacheScope = "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/elasticache/aws4_request, Signature=x"
	)

	cases := []struct {
		name   string
		action string
		auth   string
		want   bool
	}{
		{"ListTags under sns scope falls through", "ListTagsForResource", snsScope, false},
		{"ListTags under elasticache scope is claimed", "ListTagsForResource", cacheScope, true},
		{"AddTags under sns scope falls through", "AddTagsToResource", snsScope, false},
		{"RemoveTags under sns scope falls through", "RemoveTagsFromResource", snsScope, false},
		{"non-shared action needs no scope", "DescribeCacheClusters", snsScope, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Action=" + tc.action + "&Version=2015-02-02"
			req := httptest.NewRequest("POST", "/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Authorization", tc.auth)

			if got := h.Matches(req); got != tc.want {
				t.Fatalf("Matches(%s, scope in %q) = %v, want %v", tc.action, tc.auth, got, tc.want)
			}
		})
	}
}
