package aws

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// newRegionStamp builds a pre-dispatch hook that reads the caller's region from
// the SigV4 credential scope and stamps it on the request context, so backends
// stamp resources and ARNs with the region the client addressed rather than the
// emulator's fixed default. It always proceeds and never writes a response; an
// unsigned request carries no region, leaving the context untouched so backends
// fall back to their configured default (identical to the pre-change path).
func newRegionStamp() func(http.ResponseWriter, *http.Request) (*http.Request, bool) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, bool) {
		region := awsquery.CredentialScopeRegion(r.Header.Get("Authorization"))
		if region == "" {
			return r, true
		}

		return r.WithContext(regionctx.WithRegion(r.Context(), region)), true
	}
}
