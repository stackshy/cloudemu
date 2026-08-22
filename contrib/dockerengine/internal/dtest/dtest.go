// Package dtest holds the shared test helpers the engine e2es import: a
// docker-up skip check and the Azure ARM client plumbing (options + a fake
// credential). It is a normal (importable) package, not a _test package, so
// every engine's external test package can share one copy.
package dtest

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dockerx"
)

// dockerInfoTimeout bounds the `docker info` daemon probe.
const dockerInfoTimeout = 10 * time.Second

// DockerUp reports whether the docker CLI is present AND its daemon answers, so
// the real-Docker e2es skip cleanly on a host without a running daemon.
func DockerUp() bool {
	if !dockerx.Available() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerInfoTimeout)
	defer cancel()

	//nolint:gosec // first-party argv, never a shell string
	return exec.CommandContext(ctx, dockerx.Binary, "info").Run() == nil
}

// FakeCred is a no-op Azure token credential for the ARM test clients.
type FakeCred struct{}

// GetToken returns a static, non-expiring-in-practice token; the wire layer does
// not verify it.
func (FakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// ARMOptions builds arm.ClientOptions that point a real Azure ARM SDK client at
// the given httptest server, disabling retries so failures surface immediately.
func ARMOptions(ts *httptest.Server) *arm.ClientOptions {
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
