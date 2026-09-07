package securesourcemanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	ssmprovider "github.com/stackshy/cloudemu/v2/providers/gcp/securesourcemanager"
)

// TestCreateAcceptsSnakeCaseIDParam verifies the create id is read from either
// the camelCase query param (instanceId, what a GAPIC client sends) or its
// snake_case form (instance_id, what the Terraform google provider sends), as
// the real GCP API accepts both.
func TestCreateAcceptsSnakeCaseIDParam(t *testing.T) {
	h := New(ssmprovider.New(config.NewOptions(config.WithProjectID("p"))))

	cases := []struct{ name, query string }{
		{"camelCase", "instanceId=cam"},
		{"snake_case", "instance_id=snk"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost,
				"http://x/v1/projects/p/locations/us-central1/instances?"+tc.query,
				strings.NewReader(`{"labels":{"env":"dev"}}`))
			w := httptest.NewRecorder()

			h.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("create with %s = %d, want 200: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}
