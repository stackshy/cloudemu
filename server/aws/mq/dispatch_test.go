package mq_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/mq"
)

// TestMatches verifies the handler claims exactly the MQ path shapes and
// ARN-scoped /v1/tags paths, and never a sibling service's tag ARN or root.
func TestMatches(t *testing.T) {
	h := mq.New(nil)

	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/v1/brokers", true},                                                  // CreateBroker
		{http.MethodGet, "/v1/brokers", true},                                                   // ListBrokers
		{http.MethodGet, "/v1/brokers/b-123", true},                                             // DescribeBroker
		{http.MethodPut, "/v1/brokers/b-123", true},                                             // UpdateBroker
		{http.MethodDelete, "/v1/brokers/b-123", true},                                          // DeleteBroker
		{http.MethodPost, "/v1/brokers/b-123/reboot", true},                                     // RebootBroker
		{http.MethodGet, "/v1/brokers/b-123/users", true},                                       // ListUsers
		{http.MethodGet, "/v1/brokers/b-123/shared-resources", true},                            // DescribeSharedResources
		{http.MethodPost, "/v1/brokers/b-123/users/admin", true},                                // CreateUser
		{http.MethodPost, "/v1/configurations", true},                                           // CreateConfiguration
		{http.MethodGet, "/v1/configurations", true},                                            // ListConfigurations
		{http.MethodGet, "/v1/configurations/c-1", true},                                        // DescribeConfiguration
		{http.MethodGet, "/v1/configurations/c-1/revisions/2", true},                            // DescribeConfigurationRevision
		{http.MethodGet, "/v1/tags/arn%3Aaws%3Amq%3Aus-east-1%3A1%3Abroker%3Ab-1", true},        // ListTags (MQ ARN)
		{http.MethodPost, "/v1/tags/arn%3Aaws%3Amq%3Aus-east-1%3A1%3Abroker%3Ab-1", true},       // CreateTags
		{http.MethodGet, "/v1/tags/arn%3Aaws%3Abatch%3Aus-east-1%3A1%3Ajob-queue%2Ffoo", false}, // sibling Batch
		{http.MethodGet, "/v1/tags/arn%3Aaws%3Akafka%3Aus-east-1%3A1%3Acluster%2Ffoo", false},   // sibling Kafka
		{http.MethodGet, "/v1/apis", false},                                                     // AppSync root
		{http.MethodGet, "/some-bucket", false},                                                 // arbitrary bucket op
		{http.MethodGet, "/2015-03-31/functions", false},                                        // non-v1 root
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := h.Matches(r); got != tc.want {
			t.Errorf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
