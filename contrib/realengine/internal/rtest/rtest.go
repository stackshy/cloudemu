// Package rtest holds test helpers shared across the realengine engine test
// packages (postgres, redis, functions). It is under internal/ so it stays
// importable only within this module.
package rtest

import (
	"context"
	"net/http/httptest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// AzureFakeCred is a no-op azcore.TokenCredential for pointing Azure SDK
// clients at a local httptest server: the wire layer accepts any bearer token.
type AzureFakeCred struct{}

// GetToken returns a static, never-expiring fake access token.
func (AzureFakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// ARMOpts returns arm.ClientOptions that route an Azure Resource Manager SDK
// client at ts (a local httptest TLS server) with retries disabled.
func ARMOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}
