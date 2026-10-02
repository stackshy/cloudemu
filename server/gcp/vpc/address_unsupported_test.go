package vpc_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	awsvpc "github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/server/gcp/vpc"
)

// TestAddressesNeedTheProviderCapability: addresses live in the provider's
// GCPAddressStore, so a networking driver without it answers 501 rather than
// silently keeping them somewhere a snapshot cannot see.
func TestAddressesNeedTheProviderCapability(t *testing.T) {
	h := vpc.New(awsvpc.New(config.NewOptions()), nil)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/compute/v1/projects/p/global/addresses", strings.NewReader(`{"name":"a"}`)),
		httptest.NewRequest(http.MethodPost, "/compute/v1/projects/p/global/addresses/a/setLabels",
			strings.NewReader(`{"labelFingerprint":"x"}`)),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501 (%s)", req.Method, req.URL.Path, rec.Code, rec.Body.String())
		}
	}
}
