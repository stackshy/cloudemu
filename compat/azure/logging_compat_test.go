package azure

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	loggingService = "logging"

	logsSub = "sub-1"
	logsRG  = "rg-1"
	logsWS  = "logs-ws"
)

// TestLogAnalyticsCompat drives a real armoperationalinsights.WorkspacesClient
// against CloudEmu's in-process Azure wire server. The Log Analytics workspace
// control plane maps onto the logging driver's log-group lifecycle, so each SDK
// call records a result for the portable logging operation it exercises.
func TestLogAnalyticsCompat(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{LogAnalytics: cloudP.LogAnalytics})

	myCloud := cloud.Configuration{
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
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armoperationalinsights.NewWorkspacesClient(logsSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("NewWorkspacesClient: %v", err)
	}

	ctx := context.Background()

	sess.Op(loggingService, "CreateLogGroup", func() error {
		poller, cerr := client.BeginCreateOrUpdate(ctx, logsRG, logsWS, armoperationalinsights.Workspace{
			Location: to.Ptr("eastus"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
			Properties: &armoperationalinsights.WorkspaceProperties{
				RetentionInDays: to.Ptr(int32(30)),
			},
		}, nil)
		if cerr != nil {
			return cerr
		}

		_, cerr = poller.PollUntilDone(ctx, nil)

		return cerr
	})

	sess.Op(loggingService, "GetLogGroup", func() error {
		_, gerr := client.Get(ctx, logsRG, logsWS, nil)

		return gerr
	})

	sess.Op(loggingService, "UpdateLogGroup", func() error {
		poller, uerr := client.BeginCreateOrUpdate(ctx, logsRG, logsWS, armoperationalinsights.Workspace{
			Location: to.Ptr("eastus"),
			Properties: &armoperationalinsights.WorkspaceProperties{
				RetentionInDays: to.Ptr(int32(90)),
			},
		}, nil)
		if uerr != nil {
			return uerr
		}

		_, uerr = poller.PollUntilDone(ctx, nil)

		return uerr
	})

	sess.Op(loggingService, "ListLogGroups", func() error {
		pager := client.NewListByResourceGroupPager(logsRG, nil)
		for pager.More() {
			if _, perr := pager.NextPage(ctx); perr != nil {
				return perr
			}
		}

		return nil
	})

	sess.Op(loggingService, "DeleteLogGroup", func() error {
		poller, derr := client.BeginDelete(ctx, logsRG, logsWS, nil)
		if derr != nil {
			return derr
		}

		_, derr = poller.PollUntilDone(ctx, nil)

		return derr
	})
}
