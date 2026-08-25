package azure_test

// Dispatch-ordering regression tests (Architecture Theme 2, #590).
//
// server.Server is a first-match-wins dispatcher. In server/azure/azure.go
// New(), the BlobStorage data-plane handler is the permissive fallback — its
// Matches() claims every non-/subscriptions/ path — so it MUST register last.
// Several service handlers (Cosmos DB on /dbs, Key Vault on /secrets, ACR on
// /acr/v1/…) sit on non-/subscriptions/ paths and are therefore ambiguous with
// the Blob fallback; each is documented as "register before the permissive
// BlobStorage fallback".
//
// These tests drive the FULL production server (NewFromProvider) with those
// ambiguous paths and assert the specific handler — not Blob — served. The
// robust discriminator is the "X-Ms-Version" response header: the BlobStorage
// handler sets it on every response, and the specific handlers never do. If a
// contributor moves the Blob fallback earlier (or alphabetizes registrations),
// Blob swallows these paths, stamps X-Ms-Version, and these tests fail.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func fullAzureServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// TestSpecificHandlersWinBeforeBlobFallback asserts the ARM/data-plane handlers
// on non-/subscriptions/ paths win over the permissive BlobStorage fallback.
func TestSpecificHandlersWinBeforeBlobFallback(t *testing.T) {
	ts := fullAzureServer(t)

	cases := []struct {
		name string
		path string
	}{
		{"cosmos_dbs_before_blob", "/dbs"},
		{"keyvault_secrets_before_blob", "/secrets"},
		{"acr_catalog_before_blob", "/acr/v1/_catalog"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+tc.path, nil) //nolint:noctx // short-lived test request
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}

			defer resp.Body.Close()

			_, _ = io.Copy(io.Discard, resp.Body)

			// The BlobStorage fallback stamps X-Ms-Version on every response;
			// the specific handlers never do. Its presence proves Blob wrongly
			// swallowed the request — i.e. the registration order is broken.
			if v := resp.Header.Get("X-Ms-Version"); v != "" {
				t.Errorf("GET %s was served by the permissive BlobStorage fallback (X-Ms-Version=%q); "+
					"the specific handler must register before it", tc.path, v)
			}

			// Guard against a false pass where no handler matched at all.
			if resp.StatusCode == http.StatusNotImplemented {
				t.Errorf("GET %s reached the no-handler-registered fallback (501); expected a specific handler", tc.path)
			}
		})
	}
}
