package codeartifact_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/codeartifact"
)

// TestMatches verifies the handler claims exactly the CodeArtifact path shapes,
// including the query-string style, and scopes the shared /v1/tag(s)/untag roots
// to CodeArtifact ARNs so a sibling service's tag request falls through.
func TestMatches(t *testing.T) {
	h := codeartifact.New(nil)

	const caArn = "?resourceArn=arn:aws:codeartifact:us-east-1:123456789012:domain/d"

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/v1/domain?domain=d", true},                         // CreateDomain
		{http.MethodGet, "/v1/domain?domain=d", true},                          // DescribeDomain
		{http.MethodDelete, "/v1/domain?domain=d", true},                       // DeleteDomain
		{http.MethodPost, "/v1/domains", true},                                 // ListDomains
		{http.MethodPost, "/v1/domain/repositories?domain=d", true},            // ListRepositoriesInDomain
		{http.MethodPost, "/v1/repository?domain=d&repository=r", true},        // CreateRepository
		{http.MethodGet, "/v1/repository?domain=d&repository=r", true},         // DescribeRepository
		{http.MethodPut, "/v1/repository?domain=d&repository=r", true},         // UpdateRepository
		{http.MethodDelete, "/v1/repository?domain=d&repository=r", true},      // DeleteRepository
		{http.MethodPost, "/v1/repositories", true},                            // ListRepositories
		{http.MethodPost, "/v1/repository/external-connection?domain=d", true}, // AssociateExternalConnection
		{http.MethodPost, "/v1/tag" + caArn, true},                             // TagResource (CodeArtifact ARN)
		{http.MethodPost, "/v1/tags" + caArn, true},                            // ListTagsForResource
		{http.MethodPost, "/v1/untag" + caArn, true},                           // UntagResource
		// Shared tag roots with a NON-CodeArtifact ARN fall through.
		{http.MethodPost, "/v1/tags?resourceArn=arn:aws:mq:us-east-1:1:broker:b-1", false},
		{http.MethodGet, "/v1/tags/arn%3Aaws%3Amq%3Aus-east-1%3A1%3Abroker%3Ab-1", false}, // MQ path-style tags
		{http.MethodPost, "/v1/brokers", false},                                           // MQ root
		{http.MethodGet, "/v1/apis", false},                                               // AppSync root
		{http.MethodGet, "/some-bucket", false},                                           // arbitrary bucket op
		{http.MethodGet, "/2015-03-31/functions", false},                                  // non-v1 root
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
