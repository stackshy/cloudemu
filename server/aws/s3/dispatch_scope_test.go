package s3_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	s3srv "github.com/stackshy/cloudemu/v2/server/aws/s3"
)

// TestMatchesDeclinesForeignCredentialScope guards the dispatch fix: S3 shares the
// single wire endpoint with every other REST service and has no distinguishing
// path prefix, so it must not act as a blind REST catch-all. A request whose SigV4
// credential scope names another service must NOT be claimed by S3 (otherwise the
// op leaks here and is answered with a bogus NoSuchBucket / false 200); an s3-scoped
// or unsigned request still is.
func TestMatchesDeclinesForeignCredentialScope(t *testing.T) {
	h := s3srv.New(cloudemu.NewAWS().S3)

	cred := func(service string) string {
		return "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/" + service + "/aws4_request, " +
			"SignedHeaders=host, Signature=deadbeef"
	}

	cases := []struct {
		name string
		auth string
		want bool
	}{
		{"s3-signed is claimed", cred("s3"), true},
		{"unsigned falls to s3 (path-style)", "", true},
		{"lambda-signed is declined", cred("lambda"), false},
		{"eks-signed is declined", cred("eks"), false},
		{"backup-signed is declined", cred("backup"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A plain REST path with no X-Amz-Target / ?Action, the shape that
			// previously made S3 a catch-all regardless of the signed-for service.
			r := httptest.NewRequest(http.MethodGet, "/2016-08-19/account-settings/", nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(auth=%q) = %v, want %v", tc.auth, got, tc.want)
			}
		})
	}
}
