package mq_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/mq"
)

// authHeader builds a minimal SigV4 Authorization header whose credential scope
// names the given service, which is all CredentialScopeService reads.
func authHeader(service string) string {
	return "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20250101/us-east-1/" + service +
		"/aws4_request, SignedHeaders=host, Signature=deadbeef"
}

// TestMatchesDeclinesForeignCredentialScope guards the MQ/MSK collision fix:
// /v1/configurations is shared with Amazon MSK (Kafka), so an MQ handler
// registered first must decline a kafka-signed request (letting the kafka
// handler claim it) while still claiming its own and unsigned requests.
func TestMatchesDeclinesForeignCredentialScope(t *testing.T) {
	h := mq.New(nil)

	cases := []struct {
		name    string
		service string // "" = no Authorization header
		want    bool
	}{
		{"kafka-signed configurations declined", "kafka", false},
		{"mq-signed configurations claimed", "mq", true},
		{"unsigned configurations still claimed", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/configurations", nil)
			if tc.service != "" {
				r.Header.Set("Authorization", authHeader(tc.service))
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(/v1/configurations, scope=%q) = %v, want %v", tc.service, got, tc.want)
			}
		})
	}
}
