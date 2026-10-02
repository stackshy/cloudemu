package rds_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/rds"
)

// scopeAuth builds a minimal SigV4 Authorization header whose credential scope
// names the given service.
func scopeAuth(service string) string {
	return "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20250101/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=deadbeef"
}

// TestMatchesSharedEventVerbs checks RDS passes on event verbs meant for
// ElastiCache or Redshift, and keeps its own and unsigned ones.
func TestMatchesSharedEventVerbs(t *testing.T) {
	h := rds.New(nil)

	cases := []struct {
		name    string
		action  string
		service string // "" means unsigned
		version string // "" means no Version field
		want    bool
	}{
		{"rds-signed DescribeEvents", "DescribeEvents", "rds", "2014-10-31", true},
		{"elasticache-signed DescribeEvents", "DescribeEvents", "elasticache", "2015-02-02", false},
		{"redshift-signed DescribeEvents", "DescribeEvents", "redshift", "2012-12-01", false},
		{"unsigned DescribeEvents", "DescribeEvents", "", "", true},
		{"unsigned DescribeEvents rds version", "DescribeEvents", "", "2014-10-31", true},
		{"unsigned DescribeEvents elasticache version", "DescribeEvents", "", "2015-02-02", false},
		{"rds-signed CreateEventSubscription", "CreateEventSubscription", "rds", "2014-10-31", true},
		{"elasticache-signed CreateEventSubscription", "CreateEventSubscription", "elasticache", "", false},
		{"redshift-signed CreateEventSubscription", "CreateEventSubscription", "redshift", "", false},
		{"unsigned CreateEventSubscription", "CreateEventSubscription", "", "", true},
		{"redshift-signed DescribeEventCategories", "DescribeEventCategories", "redshift", "", false},
		{"non-shared verb ignores version", "DescribeDBInstances", "", "2015-01-01", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Action=" + tc.action
			if tc.version != "" {
				body += "&Version=" + tc.version
			}

			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			if tc.service != "" {
				r.Header.Set("Authorization", scopeAuth(tc.service))
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(%s, scope=%q, version=%q) = %v, want %v",
					tc.action, tc.service, tc.version, got, tc.want)
			}
		})
	}
}
