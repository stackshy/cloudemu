package compat

import (
	"net/http"
	"net/http/httptest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const providerAzure = "azure"

// AzureSession is a Session backed by CloudEmu's Azure wire server plus a real
// azure-sdk-for-go client transport pointed at it.
type AzureSession struct {
	*Session

	transport *http.Client
}

// BootAzure starts CloudEmu's Azure wire server in-process for the given
// drivers and returns a session. Blob data-plane needs no credentials, so the
// SDK client uses the test server's transport with retries disabled.
//
//nolint:gocritic // by-value Drivers mirrors azureserver.New's ergonomic API
func BootAzure(tb TB, d azureserver.Drivers) *AzureSession {
	tb.Helper()

	srv := azureserver.New(d)
	ts := httptest.NewServer(srv)
	tb.Cleanup(ts.Close)

	s := &Session{tb: tb, provider: providerAzure, endpoint: ts.URL}
	tb.Cleanup(s.flush)

	return &AzureSession{Session: s, transport: ts.Client()}
}

// BlobClient returns a real azblob client pointed at the emulator (anonymous
// access; retries disabled so the first response is asserted).
func (a *AzureSession) BlobClient() (*azblob.Client, error) {
	opts := &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: a.transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	return azblob.NewClientWithNoCredential(a.endpoint+"/", opts)
}
