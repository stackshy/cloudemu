package elasticache_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/elasticache"
)

// TestMatchesDescribeEvents checks ElastiCache claims its own DescribeEvents
// and passes on ones meant for RDS or Redshift.
func TestMatchesDescribeEvents(t *testing.T) {
	h := elasticache.New(nil)

	cases := []struct {
		name    string
		service string // "" means unsigned
		version string // "" means no Version field
		want    bool
	}{
		{"elasticache-signed", "elasticache", "2015-02-02", true},
		{"rds-signed", "rds", "2014-10-31", false},
		{"redshift-signed", "redshift", "2012-12-01", false},
		{"unsigned no version", "", "", true},
		{"unsigned elasticache version", "", "2015-02-02", true},
		{"unsigned rds version", "", "2014-10-31", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Action=DescribeEvents"
			if tc.version != "" {
				body += "&Version=" + tc.version
			}

			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			if tc.service != "" {
				r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/"+
					tc.service+"/aws4_request, Signature=x")
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(DescribeEvents, scope=%q, version=%q) = %v, want %v",
					tc.service, tc.version, got, tc.want)
			}
		})
	}
}
