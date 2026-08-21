package compat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	providerAzure = "azure"

	fakeTokenTTL = time.Hour
)

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

// BootAzureTLS is BootAzure over a TLS test server, for Azure services whose
// SDKs refuse bearer tokens over plaintext (ARM/token data planes). Pair it
// with FakeAzureCred and Transport().
//
//nolint:gocritic // by-value Drivers mirrors azureserver.New's ergonomic API
func BootAzureTLS(tb TB, d azureserver.Drivers) *AzureSession {
	tb.Helper()

	srv := azureserver.New(d)
	ts := httptest.NewTLSServer(srv)
	tb.Cleanup(ts.Close)

	s := &Session{tb: tb, provider: providerAzure, endpoint: ts.URL}
	tb.Cleanup(s.flush)

	return &AzureSession{Session: s, transport: ts.Client()}
}

// Transport returns the test server's HTTP client, for wiring into an SDK's
// policy.ClientOptions.Transport.
func (a *AzureSession) Transport() *http.Client { return a.transport }

// fakeAzureCred is a static token credential; the emulator does not verify
// tokens, so any non-empty token is accepted.
type fakeAzureCred struct{}

func (fakeAzureCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(fakeTokenTTL)}, nil
}

// FakeAzureCred returns a static credential for Azure SDK clients pointed at
// the emulator.
func FakeAzureCred() azcore.TokenCredential { return fakeAzureCred{} }

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
