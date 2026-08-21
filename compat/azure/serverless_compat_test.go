package azure

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	serverlessService = "serverless"

	fnSubID  = "00000000-0000-0000-0000-000000000000"
	fnRGName = "test-rg"
	fnName   = "compat-fn"
	fnRegion = "eastus"
	fnKind   = "functionapp"
	fnPollMS = time.Millisecond
)

// newFunctionsClient builds a real armappservice WebAppsClient pointed at the
// emulator's Azure wire server over the harness TLS transport.
func newFunctionsClient(t *testing.T, sess *compat.AzureSession) *armappservice.WebAppsClient {
	t.Helper()

	emuCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     emuCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	factory, err := armappservice.NewClientFactory(fnSubID, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return factory.NewWebAppsClient()
}

// TestFunctionsCompat drives the real Azure App Service SDK against CloudEmu's
// in-process Functions wire server and records one compat result per routed
// portable serverless op: CreateFunction, GetFunction, ListFunctions,
// DeleteFunction.
func TestFunctionsCompat(t *testing.T) {
	p := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{Functions: p.Functions})
	client := newFunctionsClient(t, sess)
	ctx := context.Background()

	pollOpts := runtime.PollUntilDoneOptions{Frequency: fnPollMS}

	sess.Op(serverlessService, "CreateFunction", func() error {
		poller, err := client.BeginCreateOrUpdate(ctx, fnRGName, fnName,
			armappservice.Site{
				Kind:     to.Ptr(fnKind),
				Location: to.Ptr(fnRegion),
				Properties: &armappservice.SiteProperties{
					HTTPSOnly: to.Ptr(true),
					SiteConfig: &armappservice.SiteConfig{
						LinuxFxVersion: to.Ptr("Python|3.10"),
					},
				},
			},
			nil,
		)
		if err != nil {
			return err
		}

		_, err = poller.PollUntilDone(ctx, &pollOpts)

		return err
	})

	sess.Op(serverlessService, "GetFunction", func() error {
		_, err := client.Get(ctx, fnRGName, fnName, nil)

		return err
	})

	sess.Op(serverlessService, "ListFunctions", func() error {
		pager := client.NewListByResourceGroupPager(fnRGName, nil)
		for pager.More() {
			if _, err := pager.NextPage(ctx); err != nil {
				return err
			}
		}

		return nil
	})

	sess.Op(serverlessService, "DeleteFunction", func() error {
		_, err := client.Delete(ctx, fnRGName, fnName, nil)

		return err
	})
}
